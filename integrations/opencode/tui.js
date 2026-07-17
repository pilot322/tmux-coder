// tmux-coder OpenCode TUI plugin.
//
// Activity reporting runs in each TUI process rather than in OpenCode's server.
// This keeps TMUX_CODER_AGENT_ID pane-specific when several attached TUIs share
// one server. The server broadcasts all session events, so this plugin reports
// only the displayed session and its descendants.

import { appendFileSync } from "node:fs";

const AGENT_ID = process.env.TMUX_CODER_AGENT_ID;
const DEBUG = process.env.TMUX_CODER_PLUGIN_DEBUG;

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

export async function TmuxCoderStatus(api) {
  if (!AGENT_ID) return;

  const eventURL = `${daemonBaseURL(process.env.TMUX_CODERD_ADDR)}/agents/${AGENT_ID}/event`;
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
