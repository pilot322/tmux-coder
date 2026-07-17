// tmux-coder OpenCode TUI plugin.
//
// Activity reporting runs in each TUI process rather than in OpenCode's server.
// This keeps TMUX_CODER_AGENT_ID pane-specific when several attached TUIs share
// one server. The server broadcasts all session events, so only events for the
// session currently displayed by this TUI are reported.

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
  let blockedSession = "";

  function currentSessionID() {
    const route = api.route.current;
    return route?.name === "session" ? route.params?.sessionID : undefined;
  }

  function isCurrent(event) {
    const sessionID = eventSessionID(event);
    return sessionID && sessionID === currentSessionID();
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

  function on(type, handler) {
    api.event.on(type, (event) => {
      debug(`event ${event?.type} session=${eventSessionID(event) ?? "none"}`);
      if (!isCurrent(event)) return;
      handler(event);
    });
  }

  report("idle");

  on("session.status", (event) => {
    const sessionID = event.properties.sessionID;
    const status = event.properties.status.type;
    if (status === "idle") {
      blockedSession = "";
      report("idle");
    } else if (blockedSession !== sessionID) {
      report("busy");
    }
  });
  on("session.idle", () => {
    blockedSession = "";
    report("idle");
  });

  for (const type of ["permission.asked", "question.asked"]) {
    on(type, (event) => {
      blockedSession = event.properties.sessionID;
      report("waiting");
    });
  }
  for (const type of [
    "permission.replied",
    "question.replied",
    "question.rejected",
  ]) {
    on(type, () => {
      blockedSession = "";
      report("busy");
    });
  }
}

export default {
  id: "tmux-coder-status",
  tui: TmuxCoderStatus,
};
