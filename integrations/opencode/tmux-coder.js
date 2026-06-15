// tmux-coder OpenCode plugin.
//
// When OpenCode runs inside a tmux-coder-managed pane, this plugin reports the
// agent's activity to the tmux-coder daemon so its status shows up in the TUI.
// It translates OpenCode's native event bus into tmux-coder's canonical,
// agent-agnostic vocabulary (busy / idle / waiting) and POSTs it to
// /agents/{id}/event.
//
// It knows it is wrapped by tmux-coder solely from TMUX_CODER_AGENT_ID; when that
// is unset the plugin is inert. Reporting is fire-and-forget with a short
// timeout, and every error is swallowed: a daemon that is down, restarting, or
// no longer recognises this agent id must never hang or crash OpenCode.

import { appendFileSync } from "node:fs";

const AGENT_ID = process.env.TMUX_CODER_AGENT_ID;

// When TMUX_CODER_PLUGIN_DEBUG names a file, append a trace line per event and
// POST. Off by default; diagnostics only.
const DEBUG = process.env.TMUX_CODER_PLUGIN_DEBUG;
function debug(line) {
  if (!DEBUG) return;
  try {
    appendFileSync(DEBUG, line + "\n");
  } catch {}
}

// daemonBaseURL normalises a daemon address into a full URL, mirroring
// agentwrapper.DaemonBaseURL on the Go side.
function daemonBaseURL(raw) {
  if (!raw) return "http://127.0.0.1:64357";
  if (raw.includes("://")) return raw;
  return "http://" + raw;
}

// A permission prompt or a question parks the agent on the human: surface the
// attention-grabbing waiting status. The matching reply (or, for a question, a
// dismissal) releases the agent back to work.
const WAITING_EVENTS = new Set(["permission.asked", "question.asked"]);
const REPLY_EVENTS = new Set([
  "permission.replied",
  "question.replied",
  "question.rejected",
]);

// statusByEvent maps the remaining OpenCode event types to status words. An
// event not listed here leaves the reported status unchanged.
//
// message.updated is deliberately absent: it fires both during generation and
// once more after session.idle to finalize the assistant message, so treating
// it as busy would undo idle at the end of every turn. message.part.updated
// (streaming) and the chat.message/tool hooks cover busy without that trailing
// edge.
const statusByEvent = {
  "session.idle": "idle",
  "message.part.updated": "busy",
};

export const TmuxCoderStatus = async () => {
  if (!AGENT_ID) return {};

  const eventURL = `${daemonBaseURL(process.env.TMUX_CODERD_ADDR)}/agents/${AGENT_ID}/event`;

  // Track the last status we attempted to send. message.part.updated fires per
  // token, so without this guard a busy agent would POST on every token. We
  // record the intended status optimistically (before the request resolves) so
  // a failing daemon cannot reopen that per-token spam.
  let lastStatus = "";

  // While blocked on a permission prompt or a question, the streaming/tool busy
  // signals are ignored so a token arriving mid-prompt cannot clobber waiting;
  // only the reply or session.idle releases it.
  let blocked = false;

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

  // The agent is up and waiting for the user as soon as the plugin loads.
  report("idle");

  return {
    event: async ({ event }) => {
      const type = event?.type;
      debug(`event ${type}`);

      if (WAITING_EVENTS.has(type)) {
        blocked = true;
        report("waiting");
        return;
      }
      if (REPLY_EVENTS.has(type)) {
        blocked = false;
        report("busy");
        return;
      }

      const status = statusByEvent[type];
      if (!status) return;
      if (status === "idle") blocked = false; // the turn really ended
      if (blocked) return; // streaming must not clobber waiting
      report(status);
    },
    // chat.message fires when the user submits a turn, and tool.execute.before
    // when a tool runs — both mean the agent is working. These are dedicated
    // hooks, not event-bus types.
    "chat.message": async () => {
      if (!blocked) report("busy");
    },
    "tool.execute.before": async () => {
      if (!blocked) report("busy");
    },
  };
};
