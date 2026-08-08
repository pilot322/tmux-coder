// tmux-coder OpenCode TUI plugin.
//
// Activity reporting runs in each TUI process rather than in OpenCode's server.
// This keeps TMUX_CODER_AGENT_ID pane-specific when several attached TUIs share
// one server. The server broadcasts all session events, so this plugin reports
// only the displayed session and its descendants.

import { appendFileSync } from "node:fs";

const AGENT_ID = process.env.TMUX_CODER_AGENT_ID;
const SETUP_REQUESTED = process.env.TMUX_CODER_AGENT_SETUP === "1";
const YOLO = process.env.TMUX_CODER_AGENT_YOLO === "1";
const REQUESTED_MODEL = process.env.TMUX_CODER_AGENT_MODEL ?? "";
const REQUESTED_VARIANT = process.env.TMUX_CODER_AGENT_VARIANT ?? "";
const DEBUG = process.env.TMUX_CODER_PLUGIN_DEBUG;
const TESTED_OPENCODE_VERSION = "1.18.14";

function debug(line) {
  if (!DEBUG) return;
  try {
    appendFileSync(DEBUG, line + "\n");
  } catch {}
}

function daemonBaseURL(raw) {
  if (!raw) return "http://127.0.0.1:64357";
  if (raw.includes("://")) return raw;
  return "http://" + raw;
}

function eventSessionID(event) {
  return (
    event?.properties?.sessionID ??
    event?.properties?.part?.sessionID ??
    event?.properties?.info?.id
  );
}

async function waitForState(api) {
  const deadline = Date.now() + 25000;
  while (!api.state?.ready) {
    if (Date.now() >= deadline) throw new Error("OpenCode TUI catalog did not become ready");
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
}

async function postSetupReady(url, body) {
  const response = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error(`tmux-coder setup handshake failed (${response.status})`);
}

async function reportSetupReady(api, setupURL) {
  if (!SETUP_REQUESTED) return;
  const version = api.app?.version ?? "unknown";
  let validated = false;
  try {
    await waitForState(api);
    if (version !== TESTED_OPENCODE_VERSION) {
      api.ui?.toast?.({
        variant: "warning",
        message: `tmux-coder tested OpenCode ${TESTED_OPENCODE_VERSION}; attempting startup automation with ${version}`,
        duration: 5000,
      });
    }

    const ready = { version };
    if (REQUESTED_MODEL) {
      const slash = REQUESTED_MODEL.indexOf("/");
      const providerID = REQUESTED_MODEL.slice(0, slash);
      const modelID = REQUESTED_MODEL.slice(slash + 1);
      const provider = api.state.provider.find((item) => item.id === providerID);
      const model = provider?.models?.[modelID];
      if (!provider || !model || (providerID === "opencode" && modelID.includes("-nano"))) {
        throw new Error(`requested model ${REQUESTED_MODEL} is unavailable`);
      }

      const displayName = model.name ?? modelID;
	  const variants = Object.keys(model.variants ?? {});
	  if (REQUESTED_VARIANT && !variants.includes(REQUESTED_VARIANT)) {
		throw new Error(`requested variant ${REQUESTED_VARIANT} is unavailable for model ${REQUESTED_MODEL}`);
	  }
      let displayMatches = 0;
      for (const candidateProvider of api.state.provider) {
        for (const [candidateID, candidate] of Object.entries(candidateProvider.models ?? {})) {
          if ((candidate.name ?? candidateID) === displayName) displayMatches++;
        }
      }
      if (displayMatches !== 1) {
        throw new Error(
          `requested model ${REQUESTED_MODEL} maps to non-unique picker display name ${JSON.stringify(displayName)}`,
        );
      }

      Object.assign(ready, {
        model: REQUESTED_MODEL,
		...(REQUESTED_VARIANT ? { variant: REQUESTED_VARIANT } : {}),
        displayName,
        statePath: process.env.TMUX_CODER_OPENCODE_STATE_PATH ?? "",
		hasVariants: variants.length > 0,
      });
    }
    await postSetupReady(setupURL, ready);
    validated = true;
    if (REQUESTED_MODEL) {
      if (api.keymap?.dispatchCommand) api.keymap.dispatchCommand("model.list");
      else if (api.command?.trigger) api.command.trigger("model.list");
      else throw new Error("OpenCode TUI model picker API is unavailable");
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    await postSetupReady(setupURL.replace(/\/ready$/, "/opened"), {});
  } catch (error) {
    await postSetupReady(validated ? setupURL.replace(/\/ready$/, "/opened") : setupURL, {
      version,
      error: error instanceof Error ? error.message : String(error),
    });
  }
}

export async function TmuxCoderStatus(api) {
  if (!AGENT_ID) return;

  const eventURL = `${daemonBaseURL(process.env.TMUX_CODERD_ADDR)}/agents/${AGENT_ID}/event`;
  const setupURL = `${daemonBaseURL(process.env.TMUX_CODERD_ADDR)}/agents/${AGENT_ID}/opencode-setup/ready`;
  let lastStatus = "";
  const parentBySession = new Map();
  const statusBySession = new Map();

  function currentSessionID() {
    const route = api.route.current;
    return route?.name === "session" ? route.params?.sessionID : undefined;
  }

  function isRelated(sessionID) {
    const root = currentSessionID();
    const seen = new Set();
    while (sessionID && !seen.has(sessionID)) {
      if (sessionID === root) return true;
      seen.add(sessionID);
      sessionID = parentBySession.get(sessionID);
    }
    return false;
  }

  function report(status) {
    debug(`report ${status} (last=${lastStatus})`);
    if (status === lastStatus) return;
    lastStatus = status;

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 1000);
    fetch(eventURL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ event: status }),
      signal: controller.signal,
    })
      .catch(() => {})
      .finally(() => clearTimeout(timer));
  }

  function reportAggregateStatus() {
    let aggregate = "idle";
    for (const [sessionID, status] of statusBySession) {
      if (!isRelated(sessionID)) continue;
      if (status === "waiting") return report("waiting");
      if (status === "busy") aggregate = "busy";
    }
    report(aggregate);
  }

  function on(type, handler) {
    api.event.on(type, (event) => {
      debug(`event ${event?.type} session=${eventSessionID(event) ?? "none"}`);
      if (!isRelated(eventSessionID(event))) return;
      handler(event);
    });
  }

  function onSessionTopology(type) {
    api.event.on(type, (event) => {
      const info = event.properties?.info;
      if (!info?.id) return;
      if (type === "session.deleted") {
        parentBySession.delete(info.id);
        statusBySession.delete(info.id);
      } else if (info.parentID) {
        parentBySession.set(info.id, info.parentID);
      } else {
        parentBySession.delete(info.id);
      }
    });
  }

  await reportSetupReady(api, setupURL);
  if (YOLO) {
    await waitForState(api);
    if (api.keymap?.dispatchCommand) api.keymap.dispatchCommand("permission.mode");
    else if (api.command?.trigger) api.command.trigger("permission.mode");
    else throw new Error("OpenCode TUI permission mode API is unavailable");
  }
  report("idle");

  onSessionTopology("session.created");
  onSessionTopology("session.updated");
  onSessionTopology("session.deleted");

  on("session.status", (event) => {
    const sessionID = event.properties.sessionID;
    const status = event.properties.status.type;
    if (status === "idle") {
      statusBySession.set(sessionID, "idle");
    } else if (statusBySession.get(sessionID) !== "waiting") {
      statusBySession.set(sessionID, "busy");
    }
    reportAggregateStatus();
  });
  on("session.idle", (event) => {
    statusBySession.set(event.properties.sessionID, "idle");
    reportAggregateStatus();
  });

  for (const type of ["permission.asked", "question.asked"]) {
    on(type, (event) => {
      statusBySession.set(event.properties.sessionID, "waiting");
      reportAggregateStatus();
    });
  }
  for (const type of [
    "permission.replied",
    "question.replied",
    "question.rejected",
  ]) {
    on(type, (event) => {
      statusBySession.set(event.properties.sessionID, "busy");
      reportAggregateStatus();
    });
  }
}

export default {
  id: "tmux-coder-status",
  tui: TmuxCoderStatus,
};
