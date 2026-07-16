import assert from "node:assert/strict";
import test from "node:test";

test("a finished subagent does not mark the TC agent idle", async () => {
  const originalAgentID = process.env.TMUX_CODER_AGENT_ID;
  const originalFetch = globalThis.fetch;
  const reported = [];

  process.env.TMUX_CODER_AGENT_ID = "42";
  globalThis.fetch = async (_url, options) => {
    reported.push(JSON.parse(options.body).event);
  };

  try {
    const { TmuxCoderStatus } = await import("./tmux-coder.js?subagent-test");
    const hooks = await TmuxCoderStatus();

    await hooks["chat.message"]();
    await hooks.event({
      event: {
        type: "session.created",
        properties: {
          info: { id: "child", parentID: "primary" },
        },
      },
    });
    await hooks.event({
      event: {
        type: "session.idle",
        properties: { sessionID: "child" },
      },
    });

    assert.deepEqual(reported, ["idle", "busy"]);

    await hooks.event({
      event: {
        type: "session.idle",
        properties: { sessionID: "primary" },
      },
    });

    assert.deepEqual(reported, ["idle", "busy", "idle"]);
  } finally {
    if (originalAgentID === undefined) delete process.env.TMUX_CODER_AGENT_ID;
    else process.env.TMUX_CODER_AGENT_ID = originalAgentID;
    globalThis.fetch = originalFetch;
  }
});
