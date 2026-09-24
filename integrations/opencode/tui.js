// CLI-only: one instance per pane, even when all panes share a v2 server.
import { Plugin } from "@opencode/plugin/tui";
import { appendFileSync } from "node:fs";

let lastEpoch = 0;
const nextEpoch = () => (lastEpoch = Math.max(Date.now(), lastEpoch + 1));
const baseURL = (raw) => !raw ? "http://127.0.0.1:64357" : raw.includes("://") ? raw : `http://${raw}`;
const sessionID = (event) => event?.data?.sessionID ?? event?.data?.session?.id ?? event?.data?.info?.id;

export function TmuxCoderStatus(context) {
  const agentID = process.env.TMUX_CODER_AGENT_ID;
  if (!agentID) return () => {};
  const paneID = process.env.TMUX_CODER_PANE_ID;
  const yolo = process.env.TMUX_CODER_AGENT_YOLO === "1";
  const base = `${baseURL(process.env.TMUX_CODERD_ADDR)}/agents/${agentID}`;
  const debug = (line) => {
    if (process.env.TMUX_CODER_PLUGIN_DEBUG) {
      try { appendFileSync(process.env.TMUX_CODER_PLUGIN_DEBUG, line + "\n"); } catch {}
    }
  };
  const stops = [];
  const parent = new Map();
  const active = new Map();
  const waits = new Map();
  const statusRequests = new Set();
  let lastStatus = "";
  let disposed = false;
  let lastRoute;
  let pending;
  let inFlight = false;
  let controller;
  let timeout;
  let retry;
  let backoff = 250;
  const setupAbort = new AbortController();
  let sequence = 0;
  const epoch = nextEpoch();

  const current = () => {
    const route = context.ui.router.current();
    return route?.type === "session" ? route.sessionID : undefined;
  };
  const related = (id) => {
    const root = current();
    const visited = new Set();
    while (id && !visited.has(id)) {
      if (id === root) return true;
      visited.add(id);
      id = parent.get(id) ?? context.data.session.get(id)?.parentID;
    }
    return false;
  };

  function sendIdentity() {
    if (disposed || inFlight || retry !== undefined || pending === undefined) return;
    const id = pending;
    pending = undefined;
    inFlight = true;
    controller = new AbortController();
    timeout = setTimeout(() => controller.abort(), 1000);
    let failed = false;
    fetch(`${base}/opencode-session`, {
      method: "PUT", headers: { "Content-Type": "application/json" }, signal: controller.signal,
      body: JSON.stringify({ sessionId: id, tmuxPaneId: paneID, reporterEpoch: epoch, sequence: ++sequence }),
    }).then((response) => { if (!response.ok) throw Error(`identity HTTP ${response.status}`); })
      .catch(() => { failed = true; if (!disposed && pending === undefined) pending = id; })
      .finally(() => {
        clearTimeout(timeout);
        controller = undefined;
        timeout = undefined;
        inFlight = false;
        if (disposed) return;
        if (failed) {
          retry = setTimeout(() => { retry = undefined; sendIdentity(); }, backoff);
          backoff = Math.min(backoff * 2, 1000);
        } else { backoff = 250; sendIdentity(); }
      });
  }
  function observe() {
    if (disposed) return;
    const id = current() ?? null;
    if (id !== lastRoute) {
      lastRoute = id;
      pending = id;
      sendIdentity();
      aggregate();
    }
  }
  function report(value) {
    if (disposed || value === lastStatus) return;
    lastStatus = value;
    debug(`report ${value}`);
    const abort = new AbortController();
    const timer = setTimeout(() => abort.abort(), 1000);
    const request = { abort, timer };
    statusRequests.add(request);
    fetch(`${base}/event`, { method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ event: value }), signal: abort.signal }).catch(() => {}).finally(() => {
      clearTimeout(timer);
      statusRequests.delete(request);
    });
  }
  function aggregate() {
    let status = "idle";
    for (const [id, requests] of waits) if (related(id) && requests.size) return report("waiting");
    for (const [id, busy] of active) if (related(id) && busy) status = "busy";
    report(status);
  }
  function on(type, handler) {
    stops.push(context.data.on(type, (event) => {
      debug(`event ${type} session=${sessionID(event) ?? "none"}`);
      handler(event.data ?? {});
    }));
  }
  on("session.created", (data) => {
    if (data.sessionID && data.parentID) parent.set(data.sessionID, data.parentID);
    aggregate();
  });
  on("session.deleted", (data) => {
    parent.delete(data.sessionID);
    active.delete(data.sessionID);
    waits.delete(data.sessionID);
    aggregate();
  });
  for (const type of ["session.execution.started", "session.execution.succeeded", "session.execution.failed", "session.execution.interrupted"]) {
    on(type, (data) => {
      if (!data.sessionID || !related(data.sessionID)) return;
      active.set(data.sessionID, type === "session.execution.started");
      aggregate();
    });
  }
  const waitKey = (kind, data) => `${kind}:${data.id ?? data.requestID}`;
  for (const [type, kind] of [["permission.asked", "permission"], ["form.created", "form"]]) {
    on(type, (raw) => {
      const data = kind === "form" ? raw.form : raw;
      if (!data.sessionID || !related(data.sessionID)) return;
      const id = data.id ?? data.requestID;
      if (id === undefined) return;
      if (kind === "permission" && yolo) {
        void context.client.permission.reply({ sessionID: data.sessionID, requestID: id, decision: "once" })
          .catch((error) => {
            debug(`permission reply failed: ${error}`);
            if (disposed) return;
            if (!waits.has(data.sessionID)) waits.set(data.sessionID, new Set());
            waits.get(data.sessionID).add(waitKey(kind, data));
            aggregate();
          });
        return;
      }
      if (!waits.has(data.sessionID)) waits.set(data.sessionID, new Set());
      waits.get(data.sessionID).add(waitKey(kind, data));
      aggregate();
    });
  }
  for (const [type, kind] of [["permission.replied", "permission"], ["form.replied", "form"], ["form.cancelled", "form"]]) {
    on(type, (data) => {
      waits.get(data.sessionID)?.delete(waitKey(kind, data));
      aggregate();
    });
  }

  observe();
  const poll = setInterval(observe, 250);
  report("idle");
  if (process.env.TMUX_CODER_AGENT_SETUP === "1") {
    void setupSession(context, base, setupAbort.signal).catch((error) => debug(`setup failed: ${error}`));
  }
  return () => {
    disposed = true;
    clearInterval(poll);
    clearTimeout(retry);
    clearTimeout(timeout);
    controller?.abort();
    setupAbort.abort();
    for (const request of statusRequests) { clearTimeout(request.timer); request.abort.abort(); }
    statusRequests.clear();
    for (const stop of stops) stop();
  };
}

async function setupSession(context, base, signal) {
  const model = process.env.TMUX_CODER_AGENT_MODEL ?? "";
  const variant = process.env.TMUX_CODER_AGENT_VARIANT ?? "";
  const post = async (path, body) => {
    const response = await fetch(`${base}/opencode-setup/${path}`, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body), signal,
    });
    if (!response.ok) throw Error(`startup handshake ${path}: HTTP ${response.status}`);
  };
  let validated = false;
  try {
    const location = context.location ?? context.data.location.default();
    let ref;
    if (model) {
       await context.data.location.model.sync(location);
       const models = context.data.location.model.list(location) ?? [];
       // V2 aliases have a distinct public id (e.g. luna-fast) and underlying modelID (luna).
       const found = models.find((item) => `${item.providerID}/${item.id}` === model && item.enabled === true);
      if (!found) throw Error(`requested model ${model} is unavailable`);
      if (variant && !(found.variants ?? []).some((item) => item.id === variant)) {
        throw Error(`requested variant ${variant} is unavailable for model ${model}`);
      }
       ref = { providerID: found.providerID, id: found.id, ...(variant ? { variant } : {}) };
    }
    await post("ready", { version: context.app.version, model, variant });
    if (signal.aborted) return;
    validated = true;
    const created = await context.client.session.create({ location, ...(ref ? { model: ref } : {}) });
    const id = created.id;
    if (!id) throw Error("OpenCode did not create a startup session");
    if (ref) await context.client.session.switchModel({ sessionID: id, model: ref });
    context.ui.router.navigate({ type: "session", sessionID: id });
    const selected = await context.client.session.get({ sessionID: id });
    if (!selected || context.ui.router.current()?.sessionID !== id ||
        (ref && (selected.model?.providerID !== ref.providerID || selected.model?.id !== ref.id ||
          (selected.model?.variant ?? "") !== variant))) {
      throw Error("OpenCode session model or displayed conversation did not match the request");
    }
    await post("opened", { model, variant });
  } catch (error) {
    if (signal.aborted) return;
    try { await post(validated ? "opened" : "ready", { error: error instanceof Error ? error.message : String(error) }); } catch {}
  }
}

export default Plugin.define({ id: "tmux-coder-status", setup: TmuxCoderStatus });
