import assert from "node:assert/strict";
import test from "node:test";

async function fixture(name) {
  const originalAgentID = process.env.TMUX_CODER_AGENT_ID;
  const originalFetch = globalThis.fetch;
  const reported = [];
  const handlers = new Map();
  const route = { name: "session", params: { sessionID: "primary" } };

  process.env.TMUX_CODER_AGENT_ID = "42";
  globalThis.fetch = async (_url, options) => {
    reported.push(JSON.parse(options.body).event);
  };

  const module = await import(`./tui.js?${name}`);
  const api = {
    route: { get current() { return route; } },
    event: {
      on(type, handler) {
        handlers.set(type, handler);
        return () => {};
      },
    },
  };
  await module.TmuxCoderStatus(api);

  return {
    module,
    reported,
    route,
    emit(type, properties) {
      handlers.get(type)?.({ type, properties });
    },
    restore() {
      if (originalAgentID === undefined) delete process.env.TMUX_CODER_AGENT_ID;
      else process.env.TMUX_CODER_AGENT_ID = originalAgentID;
      globalThis.fetch = originalFetch;
    },
  };
}

test("reports only the session displayed by this attached TUI", async () => {
  const app = await fixture("session-filter");
  try {
    assert.equal(app.module.default.id, "tmux-coder-status");
    assert.deepEqual(app.reported, ["idle"]);

    app.emit("session.status", {
      sessionID: "other-agent-session",
      status: { type: "busy" },
    });
    app.emit("session.status", {
      sessionID: "primary",
      status: { type: "busy" },
    });
    app.emit("session.idle", { sessionID: "child" });
    app.emit("session.idle", { sessionID: "primary" });

    assert.deepEqual(app.reported, ["idle", "busy", "idle"]);
  } finally {
    app.restore();
  }
});

test("waiting is not clobbered by a busy event before the reply", async () => {
  const app = await fixture("waiting");
  try {
    app.emit("permission.asked", { sessionID: "primary" });
    app.emit("session.status", {
      sessionID: "primary",
      status: { type: "busy" },
    });
    assert.deepEqual(app.reported, ["idle", "waiting"]);

    app.emit("permission.replied", { sessionID: "primary" });
    assert.deepEqual(app.reported, ["idle", "waiting", "busy"]);
  } finally {
    app.restore();
  }
});
