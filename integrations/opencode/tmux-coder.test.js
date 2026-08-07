import assert from "node:assert/strict";
import test from "node:test";

async function fixture(name, setup = {}) {
  const originalAgentID = process.env.TMUX_CODER_AGENT_ID;
  const originalSetup = process.env.TMUX_CODER_AGENT_SETUP;
  const originalModel = process.env.TMUX_CODER_AGENT_MODEL;
  const originalStatePath = process.env.TMUX_CODER_OPENCODE_STATE_PATH;
  const originalVariant = process.env.TMUX_CODER_AGENT_VARIANT;
  const originalFetch = globalThis.fetch;
  const reported = [];
  const setupBodies = [];
  const commands = [];
  const handlers = new Map();
  const route = { name: "session", params: { sessionID: "primary" } };

  process.env.TMUX_CODER_AGENT_ID = "42";
	if (setup.enabled) process.env.TMUX_CODER_AGENT_SETUP = "1";
	else delete process.env.TMUX_CODER_AGENT_SETUP;
	if (setup.model) process.env.TMUX_CODER_AGENT_MODEL = setup.model;
	else delete process.env.TMUX_CODER_AGENT_MODEL;
	if (setup.variant) process.env.TMUX_CODER_AGENT_VARIANT = setup.variant;
	else delete process.env.TMUX_CODER_AGENT_VARIANT;
	if (setup.model) process.env.TMUX_CODER_OPENCODE_STATE_PATH = "/tmp/isolated/opencode";
	else delete process.env.TMUX_CODER_OPENCODE_STATE_PATH;
  globalThis.fetch = async (_url, options) => {
	const body = JSON.parse(options.body);
	if (body.event) reported.push(body.event);
	else setupBodies.push(body);
	return { ok: true, status: 204 };
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
	app: { version: setup.version ?? "1.18.14" },
	state: {
	  ready: true,
	  path: { state: "/tmp/isolated/opencode" },
	  provider: setup.providers ?? [],
	},
	keymap: { dispatchCommand(command) { commands.push(command); } },
	ui: { toast() {} },
  };
  await module.TmuxCoderStatus(api);

  return {
    module,
    reported,
	setupBodies,
	commands,
    route,
    emit(type, properties) {
      handlers.get(type)?.({ type, properties });
    },
    restore() {
      if (originalAgentID === undefined) delete process.env.TMUX_CODER_AGENT_ID;
      else process.env.TMUX_CODER_AGENT_ID = originalAgentID;
	  if (originalSetup === undefined) delete process.env.TMUX_CODER_AGENT_SETUP;
	  else process.env.TMUX_CODER_AGENT_SETUP = originalSetup;
	  if (originalModel === undefined) delete process.env.TMUX_CODER_AGENT_MODEL;
	  else process.env.TMUX_CODER_AGENT_MODEL = originalModel;
	  if (originalStatePath === undefined) delete process.env.TMUX_CODER_OPENCODE_STATE_PATH;
	  else process.env.TMUX_CODER_OPENCODE_STATE_PATH = originalStatePath;
	  if (originalVariant === undefined) delete process.env.TMUX_CODER_AGENT_VARIANT;
	  else process.env.TMUX_CODER_AGENT_VARIANT = originalVariant;
      globalThis.fetch = originalFetch;
    },
  };
}

test("validates the exact model and opens the picker before readiness", async () => {
  const app = await fixture("setup-model", {
	enabled: true,
	model: "anthropic/claude-haiku",
	providers: [{ id: "anthropic", models: { "claude-haiku": { name: "Claude Haiku", variants: { high: {} } } } }],
  });
  try {
	assert.deepEqual(app.commands, ["model.list"]);
	assert.deepEqual(app.setupBodies, [{
	  version: "1.18.14",
	  model: "anthropic/claude-haiku",
	  displayName: "Claude Haiku",
	  statePath: "/tmp/isolated/opencode",
	  hasVariants: true,
	}, {}]);
  } finally {
	app.restore();
  }
});

test("fails closed when a picker display name is ambiguous", async () => {
  const app = await fixture("setup-ambiguous", {
	enabled: true,
	model: "anthropic/claude-haiku",
	providers: [
	  { id: "anthropic", models: { "claude-haiku": { name: "Haiku" } } },
	  { id: "other", models: { "also-haiku": { name: "Haiku" } } },
	],
  });
  try {
	assert.deepEqual(app.commands, []);
	assert.match(app.setupBodies[0].error, /non-unique picker display name/);
  } finally {
	app.restore();
  }
});

test("validates an exact requested model variant", async () => {
  const app = await fixture("setup-variant", {
	enabled: true,
	model: "openai/gpt-5.6-luna",
	variant: "high",
	providers: [{ id: "openai", models: { "gpt-5.6-luna": { name: "GPT-5.6 Luna", variants: { low: {}, high: {} } } } }],
  });
  try {
	assert.equal(app.setupBodies[0].variant, "high");
	assert.equal(app.setupBodies[0].hasVariants, true);
  } finally {
	app.restore();
  }
});

test("rejects a variant missing from the requested model", async () => {
  const app = await fixture("setup-missing-variant", {
	enabled: true,
	model: "openai/gpt-5.6-luna",
	variant: "impossible",
	providers: [{ id: "openai", models: { "gpt-5.6-luna": { variants: { low: {}, high: {} } } } }],
  });
  try {
	assert.match(app.setupBodies[0].error, /variant impossible is unavailable/);
	assert.deepEqual(app.commands, []);
  } finally {
	app.restore();
  }
});

test("aggregates activity from child sessions without accepting unrelated sessions", async () => {
  const app = await fixture("child-status");
  try {
    assert.equal(app.module.default.id, "tmux-coder-status");
    assert.deepEqual(app.reported, ["idle"]);

    app.emit("session.created", {
      info: { id: "child", parentID: "primary" },
    });
    app.emit("session.status", {
      sessionID: "other-agent-session",
      status: { type: "busy" },
    });
    app.emit("session.status", {
      sessionID: "child",
      status: { type: "busy" },
    });
    app.emit("session.status", {
      sessionID: "primary",
      status: { type: "busy" },
    });
    app.emit("session.idle", { sessionID: "child" });
    assert.deepEqual(app.reported, ["idle", "busy"]);

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

test("a child waiting for input takes precedence over a busy parent", async () => {
  const app = await fixture("child-waiting");
  try {
    app.emit("session.created", {
      info: { id: "child", parentID: "primary" },
    });
    app.emit("session.status", {
      sessionID: "primary",
      status: { type: "busy" },
    });
    app.emit("permission.asked", { sessionID: "child" });
    app.emit("session.status", {
      sessionID: "primary",
      status: { type: "busy" },
    });

    assert.deepEqual(app.reported, ["idle", "busy", "waiting"]);
  } finally {
    app.restore();
  }
});
