(function (root, factory) {
  "use strict";

  const dashboard = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = dashboard;
  } else {
    root.tmuxCoderDashboard = dashboard;
    const boot = () => dashboard.startDashboard(root.document, root.fetch.bind(root));
    if (root.document.readyState === "loading") {
      root.document.addEventListener("DOMContentLoaded", boot, { once: true });
    } else {
      boot();
    }
  }
}(typeof window === "undefined" ? globalThis : window, function () {
  "use strict";

  const API_PREFIX = "/api";
  const REFRESH_INTERVAL_MS = 1000;

  function endpoint(resource) {
    return API_PREFIX + "/" + String(resource).replace(/^\/+/, "");
  }

  function projectPayload(values, worktreeDecision) {
    const payload = { fullPath: String(values.fullPath || "").trim() };
    const title = String(values.title || "").trim();
    if (title) payload.title = title;
    if (typeof worktreeDecision === "boolean") {
      payload.createWorktreeSessions = worktreeDecision;
    }
    return payload;
  }

  function projectRequestKey(values) {
    return JSON.stringify(projectPayload(values));
  }

  function worktreePayload(values) {
    const payload = {
      projectId: Number(values.projectId),
      type: "worktree",
      branch: String(values.branch || "").trim(),
      createWorktree: true,
      createBranch: true,
    };
    if (values.origin === "source") {
      payload.parentSessionId = Number(values.sourceSessionId);
    } else {
      const baseBranch = String(values.baseBranch || "").trim();
      if (!baseBranch) throw new Error("Enter an explicit base ref for Base-ref Worktree mode.");
      payload.baseBranch = baseBranch;
    }
    return payload;
  }

  function worktreeRequestKey(values) {
    const origin = values.origin === "source" ? "source" : "base";
    return JSON.stringify({
      projectId: Number(values.projectId),
      branch: String(values.branch || "").trim(),
      origin,
      sourceSessionId: origin === "source" ? Number(values.sourceSessionId) : null,
      baseBranch: origin === "base" ? String(values.baseBranch || "").trim() : "",
    });
  }

  function worktreeRetryPayload(payload, code) {
    if (code !== "branch_exists" && code !== "worktree_exists") return null;
    const retry = Object.assign({}, payload);
    delete retry.baseBranch;
    retry.createBranch = false;
    retry.createWorktree = code === "branch_exists";
    return retry;
  }

  function secondaryPayload(values) {
    return {
      type: "secondary",
      parentSessionId: Number(values.parentSessionId),
      preferredName: String(values.preferredName || "").trim(),
      relativeWorkingDirectory: String(values.relativeWorkingDirectory || "").trim(),
      onDelete: String(values.onDelete || "cascade"),
    };
  }

  function sessionDeleteRequest(session) {
    const id = Number(session && session.id);
    const type = session && session.type === "worktree" ? "worktree" : "secondary";
    const name = session
      ? session.sessionName || session.name || "Session #" + id
      : "Unknown Session";
    if (type === "worktree") {
      return {
        resource: "sessions/" + id + "?force=true",
        title: "Delete Worktree",
        label: "Delete Worktree",
        message: "Force-delete " + name + "? This removes its Git worktree directory and can permanently discard uncommitted changes. Secondary descendants are deleted; child Worktree Sessions survive and inherit this Worktree's parent.",
      };
    }
    const policy = session && session.onDelete === "inherit" ? "inherit" : "cascade";
    return {
      resource: "sessions/" + id,
      title: "Delete Secondary",
      label: "Delete Secondary",
      message: policy === "inherit"
        ? "Delete " + name + "? Its inherit policy deletes only this Secondary Session and reparents its direct Secondary children to its parent."
        : "Delete " + name + "? Its cascade policy deletes this Secondary Session and all descendant Secondary Sessions.",
    };
  }

  function agentPayload(values) {
    const kind = String(values.kind || "").trim();
    const payload = {
      projectId: Number(values.projectId),
      sessionId: Number(values.sessionId),
      kind,
      yolo: kind === "opencode" && Boolean(values.yolo),
    };
    const displayName = String(values.displayName || "").trim();
    if (displayName) payload.displayName = displayName;
    if (kind === "opencode") {
      const model = String(values.model || "").trim();
      const variant = String(values.variant || "").trim();
      const prompt = String(values.prompt || "");
      if (model) payload.model = model;
      if (variant) payload.variant = variant;
      if (prompt.trim()) payload.prompt = prompt;
    }
    return payload;
  }

  function canCreateAgent(sessions) {
    return Array.isArray(sessions) && sessions.length > 0;
  }

  function buildTopology(projectId, sessions) {
    const nodes = new Map();
    for (const session of sessions) {
      if (Number(session.projectId) === Number(projectId)) {
        nodes.set(Number(session.id), { session, children: [] });
      }
    }

    const roots = [];
    for (const node of nodes.values()) {
      const parentId = Number(node.session.parentSessionId ?? node.session.parent ?? -1);
      const parent = parentId > 0 ? nodes.get(parentId) : null;
      if (parent) parent.children.push(node);
      else roots.push(node);
    }

    const typeOrder = { main: 0, worktree: 1, secondary: 2 };
    const sortNodes = (items) => {
      items.sort((left, right) => {
        const byType = (typeOrder[left.session.type] ?? 9) - (typeOrder[right.session.type] ?? 9);
        if (byType) return byType;
        const leftName = left.session.sessionName || left.session.name || "";
        const rightName = right.session.sessionName || right.session.name || "";
        return leftName.localeCompare(rightName);
      });
      for (const item of items) sortNodes(item.children);
    };
    sortNodes(roots);
    return roots;
  }

  function openCodeWebURL(agent) {
    if (!agent || agent.kind !== "opencode" || typeof agent.openCodeWebUrl !== "string") return "";
    const raw = agent.openCodeWebUrl.trim();
    if (!raw) return "";
    try {
      const parsed = new URL(raw);
      return parsed.protocol === "http:" || parsed.protocol === "https:" ? raw : "";
    } catch (_) {
      return "";
    }
  }

  function resourceItems(resource, body) {
    const value = body && body[resource];
    if (!Array.isArray(value)) {
      throw new Error("Malformed " + resource + " response: expected " + resource + " to be an array");
    }
    return value;
  }

  function resourceDataEqual(left, right) {
    return JSON.stringify(left) === JSON.stringify(right);
  }

  function actionFocusKey(value) {
    const data = value && value.dataset ? value.dataset : value;
    if (!data) return "";
    const action = data.action || data.focusAction || (data.openDialog ? "open-dialog:" + data.openDialog : "");
    if (!action) return "";
    return [action, data.projectId || "", data.sessionId || "", data.agentId || ""].join("|");
  }

  function createRefreshCoordinator(fetchResource, commit, reportError) {
    let active = false;
    let queued = false;
    let activePromise = null;
    let invalidationEpoch = 0;
    const resources = ["projects", "sessions", "agents"];

    async function runBatch() {
      const batchEpoch = invalidationEpoch;
      await Promise.all(resources.map(async (resource) => {
        try {
          const value = await fetchResource(resource);
          if (batchEpoch === invalidationEpoch) commit(resource, value);
        } catch (error) {
          if (batchEpoch === invalidationEpoch) reportError(resource, error);
        }
      }));
    }

    return {
      refresh() {
        if (active) {
          queued = true;
          return activePromise;
        }
        active = true;
        activePromise = (async () => {
          do {
            queued = false;
            await runBatch();
          } while (queued);
        })().finally(() => {
          active = false;
          activePromise = null;
        });
        return activePromise;
      },
      invalidate() {
        invalidationEpoch += 1;
      },
    };
  }

  class APIError extends Error {
    constructor(status, body) {
      super(body && body.error ? body.error : "Request failed with status " + status);
      this.name = "APIError";
      this.status = status;
      this.code = body && body.code ? body.code : "";
      this.worktrees = body && Array.isArray(body.worktrees) ? body.worktrees : [];
    }
  }

  function escapeHTML(value) {
    const entities = { "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" };
    return String(value ?? "").replace(/[&<>"']/g, (character) => entities[character]);
  }

  function startDashboard(doc, fetchImpl) {
    const state = {
      projects: [],
      sessions: [],
      agents: [],
      selectedProjectId: null,
      acceptedAt: null,
      refreshFailures: {},
      errors: [],
    };
    let errorSequence = 0;
    let pendingProject = null;
    let pendingProjectKey = "";
    let pendingWorktree = null;
    let pendingWorktreeKey = "";
    let confirmAction = null;
    let noticeTimer = null;
    let coordinator = null;
    const renderSignatures = new WeakMap();

    const byId = (id) => doc.getElementById(id);
    const formObject = (form) => Object.fromEntries(new doc.defaultView.FormData(form).entries());

    function replaceSectionHTML(target, html) {
      if (renderSignatures.get(target) === html) return false;
      const activeElement = doc.activeElement;
      const focusKey = activeElement && target.contains(activeElement) ? actionFocusKey(activeElement) : "";
      target.innerHTML = html;
      renderSignatures.set(target, html);
      if (focusKey) {
        const replacement = Array.from(target.querySelectorAll("[data-action], [data-focus-action], [data-open-dialog]"))
          .find((candidate) => actionFocusKey(candidate) === focusKey);
        if (replacement) replacement.focus();
      }
      return true;
    }

    async function requestJSON(resource, options) {
      const requestOptions = Object.assign({}, options || {});
      requestOptions.headers = Object.assign({ Accept: "application/json" }, requestOptions.headers || {});
      if (requestOptions.body) requestOptions.headers["Content-Type"] = "application/json";
      const mutation = requestOptions.method && requestOptions.method !== "GET";
      if (mutation && coordinator) coordinator.invalidate();
      const response = await fetchImpl(endpoint(resource), requestOptions);
      const text = await response.text();
      let body = null;
      if (text) {
        try {
          body = JSON.parse(text);
        } catch (_) {
          body = { error: text.trim() };
        }
      }
      if (!response.ok) throw new APIError(response.status, body);
      if (mutation && coordinator) coordinator.invalidate();
      return body;
    }

    function addError(message, key) {
      const errorKey = key || "error-" + (++errorSequence);
      const existing = state.errors.find((item) => item.key === errorKey);
      if (existing) {
        if (existing.message === message) return;
        existing.message = message;
      } else {
        state.errors.unshift({ key: errorKey, message });
        state.errors = state.errors.slice(0, 6);
      }
      renderErrors();
    }

    function renderErrors() {
      byId("error-region").innerHTML = state.errors.map((item) => `
        <div class="error-item">
          <span>${escapeHTML(item.message)}</span>
          <button type="button" data-dismiss-error="${escapeHTML(item.key)}" aria-label="Dismiss error">dismiss</button>
        </div>
      `).join("");
    }

    function showNotice(message) {
      const notice = byId("notice");
      notice.textContent = message;
      notice.hidden = false;
      if (noticeTimer) doc.defaultView.clearTimeout(noticeTimer);
      noticeTimer = doc.defaultView.setTimeout(() => { notice.hidden = true; }, 3500);
    }

    coordinator = createRefreshCoordinator(
      async (resource) => resourceItems(resource, await requestJSON(resource)),
      (resource, value) => {
        const changed = !resourceDataEqual(state[resource], value);
        if (changed) state[resource] = value;
        const recovered = Object.prototype.hasOwnProperty.call(state.refreshFailures, resource);
        delete state.refreshFailures[resource];
        if (recovered) {
          state.errors = state.errors.filter((item) => item.key !== "refresh-" + resource);
          renderErrors();
        }
        if (changed && resource === "projects") {
          const selectedExists = value.some((project) => Number(project.id) === Number(state.selectedProjectId));
          if (!selectedExists) state.selectedProjectId = value.length ? Number(value[0].id) : null;
        }
        state.acceptedAt = new Date();
        renderConnection();
        if (changed) renderResource(resource);
      },
      (resource, error) => {
        state.refreshFailures[resource] = error.message;
        addError(resource[0].toUpperCase() + resource.slice(1) + " refresh failed: " + error.message, "refresh-" + resource);
        renderConnection();
      },
    );

    function selectedProject() {
      return state.projects.find((project) => Number(project.id) === Number(state.selectedProjectId)) || null;
    }

    function sessionsForProject(projectId) {
      return state.sessions.filter((session) => Number(session.projectId) === Number(projectId));
    }

    function agentsForSession(sessionId) {
      return state.agents.filter((agent) => Number(agent.sessionId) === Number(sessionId));
    }

    function sessionName(session) {
      return session ? session.sessionName || session.name || "Session #" + session.id : "Unknown Session";
    }

    function projectName(project) {
      return project ? project.title || "Project #" + project.id : "Unknown Project";
    }

    function renderConnection() {
      const failures = Object.keys(state.refreshFailures);
      const card = doc.querySelector(".daemon-card");
      if (card) card.classList.toggle("is-stale", failures.length > 0);
      byId("connection-state").textContent = failures.length
        ? "Partial feed. Retaining last good " + failures.join(", ") + " data."
        : state.acceptedAt ? "Live. Stale responses are suppressed." : "Connecting to management API...";
      byId("refresh-time").textContent = state.acceptedAt
        ? state.acceptedAt.toLocaleTimeString([], { hour12: false })
        : "--:--:--";
    }

    function renderProjectList() {
      const target = byId("project-list");
      if (!state.projects.length) {
        replaceSectionHTML(target, `
          <div class="empty-state">
            <strong>No Projects yet</strong>
            <p>Open an existing directory on the Daemon host to establish a managed root.</p>
            <button class="button button-primary button-small" data-open-dialog="project" type="button">Open Project</button>
          </div>`);
        return;
      }
      replaceSectionHTML(target, state.projects.map((project) => {
        const sessions = sessionsForProject(project.id);
        const agents = state.agents.filter((agent) => Number(agent.projectId) === Number(project.id));
        const pressed = Number(project.id) === Number(state.selectedProjectId);
        return `
          <button class="project-select" type="button" data-action="select-project" data-project-id="${Number(project.id)}" aria-pressed="${pressed}">
            <span class="project-monogram">${escapeHTML(String(projectName(project)).slice(0, 2))}</span>
            <span><strong>${escapeHTML(projectName(project))}</strong><small>${escapeHTML(project.fullPath)}</small></span>
             <span class="resource-count" title="${agents.length} agents">${sessions.length}/${agents.length}</span>
          </button>`;
      }).join(""));
    }

    function sessionMeta(session) {
      if (session.type === "worktree") {
        return [session.branch, session.worktreePath].filter(Boolean).join(" / ");
      }
      if (session.type === "secondary") {
        const path = session.relativeWorkingDirectory || ".";
        return path + " / delete: " + (session.onDelete || "cascade");
      }
      return session.tmuxSessionName || "Main Project context";
    }

    function topologyNodeHTML(node) {
      const session = node.session;
      const childHTML = node.children.length
        ? `<ul>${node.children.map(topologyNodeHTML).join("")}</ul>`
        : "";
      const agentCount = agentsForSession(session.id).length;
      const worktreeButton = session.type === "main" || session.type === "worktree"
        ? `<button class="text-button" type="button" data-action="new-worktree" data-project-id="${Number(session.projectId)}" data-session-id="${Number(session.id)}">branch here</button>`
        : "";
      const deleteButton = session.type !== "main"
        ? `<button class="text-button danger" type="button" data-action="delete-session" data-session-id="${Number(session.id)}">delete</button>`
        : "";
      return `
        <li class="topology-node type-${escapeHTML(session.type)}">
          <article class="session-row">
            <span class="session-stripe" aria-hidden="true"></span>
            <div class="session-name">
              <strong>${escapeHTML(sessionName(session))}</strong>
              <span class="type-badge">${escapeHTML(session.type)}</span>
              <small>${agentCount} agent${agentCount === 1 ? "" : "s"}</small>
            </div>
            <p class="session-meta">${escapeHTML(sessionMeta(session))}</p>
            <div class="node-actions">
              ${worktreeButton}
              <button class="text-button" type="button" data-action="new-secondary" data-session-id="${Number(session.id)}">add child</button>
              <button class="text-button" type="button" data-action="new-agent" data-project-id="${Number(session.projectId)}" data-session-id="${Number(session.id)}">new agent</button>
              ${deleteButton}
            </div>
          </article>
          ${childHTML}
        </li>`;
    }

    function renderProjectDetail() {
      const target = byId("project-detail");
      const project = selectedProject();
      if (!project) {
        replaceSectionHTML(target, `
          <div class="empty-state">
            <strong>Session Topology starts with a Project</strong>
            <p>Open a Project to see its Main, Worktree, and Secondary Sessions.</p>
            <button class="button button-primary" data-open-dialog="project" type="button">Open Project</button>
          </div>`);
        return;
      }
      const topology = buildTopology(project.id, state.sessions);
      const topologyHTML = topology.length
        ? `<ul class="topology">${topology.map(topologyNodeHTML).join("")}</ul>`
        : `<div class="empty-state"><strong>No Sessions reported</strong><p>The current Project remains visible while the Session feed recovers.</p></div>`;
      replaceSectionHTML(target, `
        <header class="detail-header">
          <div class="detail-title-row">
            <div><p class="eyebrow">Project ${Number(project.id)}</p><h2>${escapeHTML(projectName(project))}</h2></div>
          </div>
          <p class="project-path">${escapeHTML(project.fullPath)}</p>
          <div class="detail-actions">
            <button class="button button-primary button-small" type="button" data-action="new-worktree" data-project-id="${Number(project.id)}">New Worktree from base</button>
            <button class="button button-quiet button-small" type="button" data-action="delete-project" data-project-id="${Number(project.id)}">Delete Project</button>
          </div>
        </header>
        <div class="topology-wrap">
          <div class="section-heading">
            <div><p class="eyebrow">Structural context</p><h2>Session Topology</h2></div>
            <p>${sessionsForProject(project.id).length} Sessions / provenance preserved</p>
          </div>
          ${topologyHTML}
        </div>`);
    }

    function formatChangedAt(value) {
      if (!value) return "change time unavailable";
      const date = new Date(value);
      if (Number.isNaN(date.getTime())) return "change time unavailable";
      return "changed " + date.toLocaleString([], { dateStyle: "medium", timeStyle: "short" });
    }

    function renderAgents() {
      const target = byId("agent-list");
      if (!state.agents.length) {
        if (!canCreateAgent(state.sessions)) {
          replaceSectionHTML(target, `
            <div class="empty-state">
              <strong>A Session is required</strong>
              <p>Open a Project first. Its Main Session will provide a context for TC Agents.</p>
              <button class="button button-primary" data-open-dialog="project" type="button">Open Project</button>
            </div>`);
          return;
        }
        replaceSectionHTML(target, `
          <div class="empty-state">
            <strong>No active TC Agents</strong>
            <p>Create a pane-backed agent in any managed Session. Exited agents leave this registry.</p>
            <button class="button button-dark" data-open-dialog="agent" type="button">Create Agent</button>
          </div>`);
        return;
      }
      replaceSectionHTML(target, state.agents.map((agent) => {
        const project = state.projects.find((item) => Number(item.id) === Number(agent.projectId)) || agent.project;
        const session = state.sessions.find((item) => Number(item.id) === Number(agent.sessionId)) || agent.session;
        const url = openCodeWebURL(agent);
        let link = "";
        if (agent.kind === "opencode") {
          link = url
            ? `<a class="opencode-link" href="${escapeHTML(url)}" target="_blank" rel="noopener noreferrer" data-focus-action="open-conversation" data-agent-id="${Number(agent.id)}">Open exact conversation</a>`
            : `<span class="link-unavailable">Conversation link unavailable</span>`;
        }
        const model = [agent.model, agent.variant].filter(Boolean).join(" / ");
        const armed = Boolean(agent.discordNotificationArmed);
        return `
          <article class="agent-card" data-status="${escapeHTML(agent.status || "unknown")}">
            <div class="agent-topline">
              <div class="agent-identity"><h3>${escapeHTML(agent.displayName || agent.kind + " #" + agent.id)}</h3><p>${escapeHTML(agent.kind)} / agent ${Number(agent.id)}</p></div>
              <span class="status-badge status-${escapeHTML(agent.status || "unknown")}">${escapeHTML(agent.status || "unknown")}</span>
            </div>
            <div class="agent-context">
              <div class="context-row"><span>Project</span><strong>${escapeHTML(projectName(project))}</strong></div>
              <div class="context-row"><span>Session</span><strong>${escapeHTML(sessionName(session))}</strong></div>
            </div>
            <p class="agent-model">${model ? escapeHTML(model) : "default model"}<br>${escapeHTML(formatChangedAt(agent.statusChangedAt))}</p>
            <div class="agent-link-row">${link}</div>
            <div class="agent-actions">
              <div class="agent-action-group">
                <button class="text-button" type="button" data-action="rename-agent" data-agent-id="${Number(agent.id)}">rename</button>
                <button class="text-button" type="button" data-action="discord-agent" data-agent-id="${Number(agent.id)}">${armed ? "disable Discord" : "arm Discord"}</button>
                <button class="text-button danger" type="button" data-action="destroy-agent" data-agent-id="${Number(agent.id)}">destroy</button>
              </div>
              ${armed ? `<span class="discord-badge">Discord armed</span>` : ""}
            </div>
           </article>`;
      }).join(""));
    }

    function updateAgentCreation() {
      const enabled = canCreateAgent(state.sessions);
      for (const button of doc.querySelectorAll("button[data-open-dialog=agent]")) {
        button.disabled = !enabled;
        button.setAttribute("aria-disabled", String(!enabled));
        if (enabled) button.removeAttribute("aria-describedby");
        else button.setAttribute("aria-describedby", "agent-create-guidance");
      }
      byId("agent-create-guidance").hidden = enabled;
    }

    function syncSelect(select, options, preferredValue) {
      const oldValue = preferredValue === undefined ? select.value : String(preferredValue ?? "");
      const signature = JSON.stringify(options);
      if (select.dataset.optionsSignature !== signature) {
        select.innerHTML = options.length
          ? options.map((option) => `<option value="${escapeHTML(option.value)}">${escapeHTML(option.label)}</option>`).join("")
          : `<option value="">No available resources</option>`;
        select.dataset.optionsSignature = signature;
      }
      if (options.some((option) => String(option.value) === oldValue)) select.value = oldValue;
    }

    function projectOptions() {
      return state.projects.map((project) => ({ value: String(project.id), label: projectName(project) + " - " + project.fullPath }));
    }

    function updateWorktreeSources(preferred) {
      const projectId = Number(byId("worktree-project").value);
      const options = sessionsForProject(projectId)
        .filter((session) => session.type === "main" || session.type === "worktree")
        .map((session) => ({ value: String(session.id), label: sessionName(session) + " (" + session.type + ")" }));
      syncSelect(byId("worktree-source"), options, preferred);
    }

    function updateAgentSessions(preferred) {
      const projectId = Number(byId("agent-project").value);
      const options = sessionsForProject(projectId).map((session) => ({
        value: String(session.id),
        label: sessionName(session) + " (" + session.type + ")",
      }));
      syncSelect(byId("agent-session"), options, preferred);
    }

    function populateForms() {
      const projects = projectOptions();
      const preferredProject = state.selectedProjectId === null ? undefined : String(state.selectedProjectId);
      syncSelect(byId("worktree-project"), projects, byId("worktree-project").value || preferredProject);
      syncSelect(byId("agent-project"), projects, byId("agent-project").value || preferredProject);
      updateWorktreeSources();
      updateAgentSessions();
      const parents = state.sessions.map((session) => {
        const project = state.projects.find((item) => Number(item.id) === Number(session.projectId));
        return { value: String(session.id), label: projectName(project) + " / " + sessionName(session) + " (" + session.type + ")" };
      });
      syncSelect(byId("secondary-parent"), parents);
      if (pendingWorktree && pendingWorktreeKey !== worktreeRequestKey(formObject(byId("worktree-form")))) {
        invalidateWorktreeDecision();
      }
    }

    function renderMetrics() {
      byId("project-count").textContent = String(state.projects.length);
      byId("session-count").textContent = String(state.sessions.length);
      byId("agent-count").textContent = String(state.agents.length);
    }

    function renderResource(resource) {
      renderMetrics();
      renderProjectList();
      renderProjectDetail();
      renderAgents();
      updateAgentCreation();
      if (resource === "projects" || resource === "sessions") populateForms();
    }

    function render() {
      renderMetrics();
      renderConnection();
      renderProjectList();
      renderProjectDetail();
      renderAgents();
      updateAgentCreation();
      populateForms();
    }

    function openDialog(dialog) {
      if (!dialog.open) dialog.showModal();
    }

    function setFormError(form, message) {
      form.querySelector("[data-form-error]").textContent = message || "";
    }

    function setBusy(form, busy) {
      for (const button of form.querySelectorAll("button")) button.disabled = busy;
      form.setAttribute("aria-busy", String(busy));
    }

    function reportMutationError(form, error) {
      const message = error instanceof Error ? error.message : String(error);
      setFormError(form, message);
      addError(message);
    }

    function closeAfterSuccess(form, message) {
      const dialog = form.closest("dialog");
      setFormError(form, "");
      if (dialog) dialog.close();
      form.reset();
      showNotice(message);
      coordinator.refresh();
    }

    function toggleWorktreeOrigin() {
      const source = byId("worktree-form").elements.origin.value === "source";
      byId("worktree-source-field").hidden = !source;
      byId("worktree-base-field").hidden = source;
      byId("worktree-source").required = source;
      byId("worktree-form").elements.baseBranch.disabled = source;
      byId("worktree-form").elements.baseBranch.required = !source;
    }

    function toggleOpenCodeFields() {
      const enabled = byId("agent-kind").value.trim() === "opencode";
      byId("opencode-fields").hidden = !enabled;
      for (const field of byId("opencode-fields").querySelectorAll("input, textarea")) field.disabled = !enabled;
    }

    function invalidateProjectDecision() {
      pendingProject = null;
      pendingProjectKey = "";
      byId("project-decision").hidden = true;
      byId("project-worktrees").innerHTML = "";
    }

    function invalidateWorktreeDecision() {
      pendingWorktree = null;
      pendingWorktreeKey = "";
      byId("worktree-conflict").hidden = true;
    }

    function openProjectDialog() {
      invalidateProjectDecision();
      setFormError(byId("project-form"), "");
      openDialog(byId("project-dialog"));
    }

    function openWorktreeDialog(projectId, sourceSessionId) {
      const form = byId("worktree-form");
      if (projectId) {
        byId("worktree-project").value = String(projectId);
        updateWorktreeSources(sourceSessionId);
      }
      const sourceRadio = form.querySelector("input[name=origin][value=source]");
      const baseRadio = form.querySelector("input[name=origin][value=base]");
      sourceRadio.checked = Boolean(sourceSessionId);
      baseRadio.checked = !sourceSessionId;
      invalidateWorktreeDecision();
      setFormError(form, "");
      toggleWorktreeOrigin();
      openDialog(byId("worktree-dialog"));
    }

    function openSecondaryDialog(parentSessionId) {
      if (parentSessionId) byId("secondary-parent").value = String(parentSessionId);
      setFormError(byId("secondary-form"), "");
      openDialog(byId("secondary-dialog"));
    }

    function openAgentDialog(projectId, sessionId) {
      if (!canCreateAgent(state.sessions)) {
        showNotice("Open a Project before creating a TC Agent.");
        return;
      }
      if (projectId) byId("agent-project").value = String(projectId);
      updateAgentSessions(sessionId);
      setFormError(byId("agent-form"), "");
      toggleOpenCodeFields();
      openDialog(byId("agent-dialog"));
    }

    function askConfirmation(title, message, confirmLabel, action, dangerous) {
      byId("confirm-title").textContent = title;
      byId("confirm-message").textContent = message;
      const submit = byId("confirm-submit");
      submit.textContent = confirmLabel;
      submit.className = "button " + (dangerous === false ? "button-primary" : "button-danger");
      confirmAction = action;
      setFormError(byId("confirm-form"), "");
      openDialog(byId("confirm-dialog"));
    }

    async function submitProject(payload, requestKey) {
      const form = byId("project-form");
      setBusy(form, true);
      setFormError(form, "");
      try {
        await requestJSON("projects", { method: "POST", body: JSON.stringify(payload) });
        invalidateProjectDecision();
        closeAfterSuccess(form, "Project opened");
      } catch (error) {
        if (error instanceof APIError && error.status === 428 && error.code === "worktrees_detected") {
          if (requestKey !== projectRequestKey(formObject(form))) {
            invalidateProjectDecision();
            setFormError(form, "Project inputs changed. Submit again to inspect the current path.");
            return;
          }
          pendingProject = payload;
          pendingProjectKey = requestKey;
          byId("project-worktrees").innerHTML = error.worktrees.map((worktree) =>
            `<li>${escapeHTML(worktree.branch)} - ${escapeHTML(worktree.path)}</li>`).join("");
          byId("project-decision").hidden = false;
          setFormError(form, "Opening needs an adopt or skip decision.");
        } else {
          reportMutationError(form, error);
        }
      } finally {
        setBusy(form, false);
      }
    }

    function showWorktreeConflict(error, payload, requestKey) {
      const retry = worktreeRetryPayload(payload, error.code);
      if (!retry) return false;
      if (requestKey !== worktreeRequestKey(formObject(byId("worktree-form")))) return false;
      pendingWorktree = retry;
      pendingWorktreeKey = requestKey;
      byId("worktree-conflict-title").textContent = error.code === "branch_exists"
        ? "Branch already exists"
        : "Worktree already exists";
      byId("worktree-conflict-message").textContent = error.message;
      byId("worktree-retry").textContent = error.code === "branch_exists"
        ? "Use existing branch"
        : "Adopt existing worktree";
      byId("worktree-conflict").hidden = false;
      return true;
    }

    async function submitWorktree(payload, requestKey) {
      const form = byId("worktree-form");
      setBusy(form, true);
      setFormError(form, "");
      try {
        await requestJSON("sessions", { method: "POST", body: JSON.stringify(payload) });
        invalidateWorktreeDecision();
        closeAfterSuccess(form, "Worktree Session created");
        toggleWorktreeOrigin();
      } catch (error) {
        if (error instanceof APIError && error.status === 409 && showWorktreeConflict(error, payload, requestKey)) {
          setFormError(form, "Choose the safe retry mode for the existing Git state.");
        } else if (requestKey !== worktreeRequestKey(formObject(form))) {
          invalidateWorktreeDecision();
          setFormError(form, "Worktree inputs changed. Submit again to check the current request.");
        } else {
          reportMutationError(form, error);
        }
      } finally {
        setBusy(form, false);
      }
    }

    byId("project-form").addEventListener("submit", (event) => {
      event.preventDefault();
      const values = formObject(event.currentTarget);
      const payload = projectPayload(values);
      const requestKey = projectRequestKey(values);
      invalidateProjectDecision();
      submitProject(payload, requestKey);
    });

    byId("worktree-form").addEventListener("submit", (event) => {
      event.preventDefault();
      invalidateWorktreeDecision();
      const values = formObject(event.currentTarget);
      try {
        const payload = worktreePayload(values);
        submitWorktree(payload, worktreeRequestKey(values));
      } catch (error) {
        setFormError(event.currentTarget, error.message);
      }
    });

    byId("secondary-form").addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = event.currentTarget;
      const payload = secondaryPayload(formObject(form));
      if (!payload.preferredName && !payload.relativeWorkingDirectory) {
        setFormError(form, "Enter a name, a relative path, or both.");
        return;
      }
      setBusy(form, true);
      setFormError(form, "");
      try {
        await requestJSON("sessions", { method: "POST", body: JSON.stringify(payload) });
        closeAfterSuccess(form, "Secondary Session created");
      } catch (error) {
        reportMutationError(form, error);
      } finally {
        setBusy(form, false);
      }
    });

    byId("agent-form").addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = event.currentTarget;
      const values = formObject(form);
      values.yolo = form.elements.yolo.checked;
      if (values.variant && !values.model) {
        setFormError(form, "OpenCode variant requires a model.");
        return;
      }
      setBusy(form, true);
      setFormError(form, "");
      try {
        await requestJSON("agents", { method: "POST", body: JSON.stringify(agentPayload(values)) });
        closeAfterSuccess(form, "TC Agent created");
        toggleOpenCodeFields();
      } catch (error) {
        reportMutationError(form, error);
      } finally {
        setBusy(form, false);
      }
    });

    byId("rename-form").addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = event.currentTarget;
      const values = formObject(form);
      setBusy(form, true);
      setFormError(form, "");
      try {
        await requestJSON("agents/" + Number(values.agentId), {
          method: "PATCH",
          body: JSON.stringify({ displayName: String(values.displayName || "").trim() }),
        });
        closeAfterSuccess(form, "TC Agent renamed");
      } catch (error) {
        reportMutationError(form, error);
      } finally {
        setBusy(form, false);
      }
    });

    byId("confirm-form").addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = event.currentTarget;
      if (!confirmAction) return;
      setBusy(form, true);
      setFormError(form, "");
      try {
        await confirmAction();
        confirmAction = null;
        form.closest("dialog").close();
      } catch (error) {
        reportMutationError(form, error);
      } finally {
        setBusy(form, false);
      }
    });

    byId("worktree-project").addEventListener("change", () => updateWorktreeSources());
    byId("agent-project").addEventListener("change", () => updateAgentSessions());
    byId("project-form").addEventListener("input", invalidateProjectDecision);
    byId("project-form").addEventListener("change", invalidateProjectDecision);
    byId("worktree-form").addEventListener("input", invalidateWorktreeDecision);
    byId("worktree-form").addEventListener("change", (event) => {
      invalidateWorktreeDecision();
      if (event.target.name === "origin") toggleWorktreeOrigin();
    });
    byId("agent-kind").addEventListener("input", toggleOpenCodeFields);
    byId("refresh-button").addEventListener("click", () => coordinator.refresh());
    byId("worktree-retry").addEventListener("click", () => {
      if (pendingWorktree) submitWorktree(pendingWorktree, pendingWorktreeKey);
    });

    doc.addEventListener("click", (event) => {
      const close = event.target.closest("[data-close-dialog]");
      if (close) {
        close.closest("dialog").close();
        return;
      }
      const dismiss = event.target.closest("[data-dismiss-error]");
      if (dismiss) {
        state.errors = state.errors.filter((item) => item.key !== dismiss.dataset.dismissError);
        renderErrors();
        return;
      }
      const decision = event.target.closest("[data-project-decision]");
      if (decision && pendingProject) {
        submitProject(projectPayload(pendingProject, decision.dataset.projectDecision === "true"), pendingProjectKey);
        return;
      }
      const opener = event.target.closest("[data-open-dialog]");
      if (opener) {
        if (opener.dataset.openDialog === "project") openProjectDialog();
        if (opener.dataset.openDialog === "agent") openAgentDialog(state.selectedProjectId);
        return;
      }
      const action = event.target.closest("[data-action]");
      if (!action) return;
      const projectId = Number(action.dataset.projectId);
      const sessionId = Number(action.dataset.sessionId);
      const agentId = Number(action.dataset.agentId);

      if (action.dataset.action === "select-project") {
        state.selectedProjectId = projectId;
        renderProjectList();
        renderProjectDetail();
        populateForms();
        return;
      }
      if (action.dataset.action === "new-worktree") {
        openWorktreeDialog(projectId || state.selectedProjectId, sessionId || null);
        return;
      }
      if (action.dataset.action === "new-secondary") {
        openSecondaryDialog(sessionId);
        return;
      }
      if (action.dataset.action === "new-agent") {
        openAgentDialog(projectId, sessionId);
        return;
      }
      if (action.dataset.action === "delete-project") {
        const project = state.projects.find((item) => Number(item.id) === projectId);
        askConfirmation(
          "Delete Project",
          "Remove " + projectName(project) + " from tmux-coder? This follows existing Project ownership semantics.",
          "Delete Project",
          async () => {
            await requestJSON("projects/" + projectId, { method: "DELETE" });
            showNotice("Project deleted");
            coordinator.refresh();
          },
        );
        return;
      }
      if (action.dataset.action === "delete-session") {
        const session = state.sessions.find((item) => Number(item.id) === sessionId);
        const deletion = sessionDeleteRequest(session);
        askConfirmation(
          deletion.title,
          deletion.message,
          deletion.label,
          async () => {
            await requestJSON(deletion.resource, { method: "DELETE" });
            showNotice("Session deleted");
            coordinator.refresh();
          },
        );
        return;
      }
      const agent = state.agents.find((item) => Number(item.id) === agentId);
      if (action.dataset.action === "rename-agent" && agent) {
        const form = byId("rename-form");
        form.elements.agentId.value = String(agent.id);
        form.elements.displayName.value = agent.displayName || "";
        setFormError(form, "");
        openDialog(byId("rename-dialog"));
        return;
      }
      if (action.dataset.action === "destroy-agent" && agent) {
        askConfirmation(
          "Destroy TC Agent",
          "Destroy " + (agent.displayName || agent.kind + " #" + agent.id) + " and its owned pane?",
          "Destroy Agent",
          async () => {
            await requestJSON("agents/" + agent.id, { method: "DELETE" });
            showNotice("TC Agent destroyed");
            coordinator.refresh();
          },
        );
        return;
      }
      if (action.dataset.action === "discord-agent" && agent) {
        const enabled = !agent.discordNotificationArmed;
        askConfirmation(
          enabled ? "Arm Discord Notification" : "Disable Discord Notification",
          enabled
            ? "Arm one notification for the next qualifying status transition from this TC Agent?"
            : "Disable the currently armed Discord Notification for this TC Agent?",
          enabled ? "Arm Notification" : "Disable Notification",
          async () => {
            await requestJSON("agents/" + agent.id + "/discord-notification", {
              method: "PUT",
              body: JSON.stringify({ enabled }),
            });
            showNotice(enabled ? "Discord Notification armed" : "Discord Notification disabled");
            coordinator.refresh();
          },
          false,
        );
      }
    });

    render();
    toggleWorktreeOrigin();
    toggleOpenCodeFields();
    coordinator.refresh();
    const interval = doc.defaultView.setInterval(() => coordinator.refresh(), REFRESH_INTERVAL_MS);
    return {
      refresh: coordinator.refresh,
      state,
      stop() {
        doc.defaultView.clearInterval(interval);
        if (noticeTimer) doc.defaultView.clearTimeout(noticeTimer);
      },
    };
  }

  return {
    API_PREFIX,
    REFRESH_INTERVAL_MS,
    actionFocusKey,
    agentPayload,
    buildTopology,
    canCreateAgent,
    createRefreshCoordinator,
    endpoint,
    escapeHTML,
    openCodeWebURL,
    projectPayload,
    projectRequestKey,
    resourceDataEqual,
    resourceItems,
    sessionDeleteRequest,
    secondaryPayload,
    startDashboard,
    worktreePayload,
    worktreeRequestKey,
    worktreeRetryPayload,
  };
}));
