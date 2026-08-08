import assert from "node:assert/strict";
import test from "node:test";

async function fixture(name, setup = {}) {
  const originalAgentID = process.env.TMUX_CODER_AGENT_ID;
  const originalPaneID = process.env.TMUX_CODER_PANE_ID;
  const originalSetup = process.env.TMUX_CODER_AGENT_SETUP;
  const originalModel = process.env.TMUX_CODER_AGENT_MODEL;
  const originalStatePath = process.env.TMUX_CODER_OPENCODE_STATE_PATH;
  const originalVariant = process.env.TMUX_CODER_AGENT_VARIANT;
  const originalYolo = process.env.TMUX_CODER_AGENT_YOLO;
  const originalFetch = globalThis.fetch;
  const originalSetInterval = globalThis.setInterval;
  const originalClearInterval = globalThis.clearInterval;
  const originalSetTimeout = globalThis.setTimeout;
  const originalClearTimeout = globalThis.clearTimeout;
  const reported = [];
  const sessionReports = [];
  const sessionAborts = [];
  const setupBodies = [];
  const commands = [];
  const permissionReplies = [];
  const handlers = new Map();
  const intervals = new Map();
  const intervalDelays = [];
  const disposeHandlers = [];
  const pendingSessionReports = [];
  const sessionResponses = [...(setup.sessionResponses ?? [])];
  const timeouts = new Map();
  const route = { name: "session", params: { sessionID: "primary" } };
  const state = {
	ready: setup.ready ?? true,
	path: { state: "/tmp/isolated/opencode" },
	provider: setup.providers ?? [],
  };

  process.env.TMUX_CODER_AGENT_ID = "42";
	process.env.TMUX_CODER_PANE_ID = "%7";
	if (setup.enabled) process.env.TMUX_CODER_AGENT_SETUP = "1";
	else delete process.env.TMUX_CODER_AGENT_SETUP;
	if (setup.model) process.env.TMUX_CODER_AGENT_MODEL = setup.model;
	else delete process.env.TMUX_CODER_AGENT_MODEL;
	if (setup.variant) process.env.TMUX_CODER_AGENT_VARIANT = setup.variant;
	else delete process.env.TMUX_CODER_AGENT_VARIANT;
	if (setup.yolo) process.env.TMUX_CODER_AGENT_YOLO = "1";
	else delete process.env.TMUX_CODER_AGENT_YOLO;
	if (setup.model) process.env.TMUX_CODER_OPENCODE_STATE_PATH = "/tmp/isolated/opencode";
	else delete process.env.TMUX_CODER_OPENCODE_STATE_PATH;
	globalThis.fetch = async (url, options) => {
	const body = JSON.parse(options.body);
	if (url.endsWith("/opencode-session")) {
	  sessionReports.push({ url, method: options.method, body });
	  if (sessionResponses.length > 0) {
		const response = sessionResponses.shift();
		if (response === "hang") {
		  return new Promise((resolve, reject) => {
			options.signal?.addEventListener("abort", () => {
			  sessionAborts.push(body.sessionId);
			  reject(options.signal.reason);
			}, { once: true });
		  });
		}
		return response;
	  }
	  if (setup.deferSessionReports) {
		return new Promise((resolve) => pendingSessionReports.push(resolve));
	  }
	}
	else if (body.event) reported.push(body.event);
	else setupBodies.push(body);
	return { ok: true, status: 204 };
  };
	globalThis.setInterval = (handler, delay) => {
	  const id = Symbol("interval");
	  intervals.set(id, handler);
	  intervalDelays.push(delay);
	  return id;
	};
	globalThis.clearInterval = (id) => intervals.delete(id);
	if (setup.controlTimeouts) {
	  globalThis.setTimeout = (handler, delay) => {
		const id = Symbol("timeout");
		timeouts.set(id, { handler, delay });
		return id;
	  };
	  globalThis.clearTimeout = (id) => timeouts.delete(id);
	}

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
	state,
	keymap: { dispatchCommand(command) { commands.push(command); } },
	client: {
	  permission: {
		reply(input) {
		  permissionReplies.push(input);
		  return Promise.resolve({ data: true });
		},
	  },
	},
	ui: { toast() {} },
	lifecycle: {
	  signal: new AbortController().signal,
	  onDispose(handler) {
		disposeHandlers.push(handler);
		return () => {};
	  },
	},
  };
  const started = module.TmuxCoderStatus(api);
  if (!setup.deferPluginStart) await started;

  return {
    module,
    reported,
	sessionReports,
	sessionAborts,
	setupBodies,
	commands,
	permissionReplies,
	intervalDelays,
	started,
    route,
	setReady() {
	  state.ready = true;
	},
	async poll() {
	  for (const handler of intervals.values()) handler();
	  await Promise.resolve();
	},
	get pendingSessionReportCount() {
	  return pendingSessionReports.length;
	},
	get activeTimeoutDelays() {
	  return [...timeouts.values()].map((timeout) => timeout.delay).sort((a, b) => a - b);
	},
	async completeSessionReport(response = { ok: true, status: 204 }) {
	  pendingSessionReports.shift()?.(response);
	  await Promise.resolve();
	  await Promise.resolve();
	  await Promise.resolve();
	},
	async runTimeout(delay) {
	  const entry = [...timeouts].find(([, timeout]) => timeout.delay === delay);
	  assert.ok(entry, `expected an active ${delay}ms timeout`);
	  const [id, timeout] = entry;
	  timeouts.delete(id);
	  timeout.handler();
	  await Promise.resolve();
	  await Promise.resolve();
	  await Promise.resolve();
	},
	async dispose() {
	  for (const handler of disposeHandlers.toReversed()) await handler();
	},
    emit(type, properties) {
      handlers.get(type)?.({ type, properties });
    },
    restore() {
      if (originalAgentID === undefined) delete process.env.TMUX_CODER_AGENT_ID;
      else process.env.TMUX_CODER_AGENT_ID = originalAgentID;
	  if (originalPaneID === undefined) delete process.env.TMUX_CODER_PANE_ID;
	  else process.env.TMUX_CODER_PANE_ID = originalPaneID;
	  if (originalSetup === undefined) delete process.env.TMUX_CODER_AGENT_SETUP;
	  else process.env.TMUX_CODER_AGENT_SETUP = originalSetup;
	  if (originalModel === undefined) delete process.env.TMUX_CODER_AGENT_MODEL;
	  else process.env.TMUX_CODER_AGENT_MODEL = originalModel;
	  if (originalStatePath === undefined) delete process.env.TMUX_CODER_OPENCODE_STATE_PATH;
	  else process.env.TMUX_CODER_OPENCODE_STATE_PATH = originalStatePath;
	  if (originalVariant === undefined) delete process.env.TMUX_CODER_AGENT_VARIANT;
	  else process.env.TMUX_CODER_AGENT_VARIANT = originalVariant;
	  if (originalYolo === undefined) delete process.env.TMUX_CODER_AGENT_YOLO;
	  else process.env.TMUX_CODER_AGENT_YOLO = originalYolo;
      globalThis.fetch = originalFetch;
	  globalThis.setInterval = originalSetInterval;
	  globalThis.clearInterval = originalClearInterval;
	  globalThis.setTimeout = originalSetTimeout;
	  globalThis.clearTimeout = originalClearTimeout;
    },
  };
}

test("reports the displayed OpenCode session immediately with pane-local identity", async () => {
  const app = await fixture("initial-session");
  try {
	assert.equal(app.sessionReports.length, 1);
	const report = app.sessionReports[0];
	assert.match(report.url, /\/agents\/42\/opencode-session$/);
	assert.equal(report.method, "PUT");
	assert.deepEqual(report.body, {
	  sessionId: "primary",
	  tmuxPaneId: "%7",
	  reporterEpoch: report.body.reporterEpoch,
	  sequence: 1,
	});
	assert.equal(typeof report.body.reporterEpoch, "number");
	assert.equal(Number.isSafeInteger(report.body.reporterEpoch), true);
	assert.ok(report.body.reporterEpoch > 0);
  } finally {
	app.restore();
  }
});

test("retries a rejected identity report when the displayed route is unchanged", async () => {
  const app = await fixture("retry-rejected-session", {
	controlTimeouts: true,
	sessionResponses: [{ ok: false, status: 503 }],
  });
  try {
	assert.deepEqual(app.sessionReports.map((report) => report.body.sessionId), ["primary"]);
	await app.runTimeout(250);
	assert.deepEqual(app.sessionReports.map((report) => report.body.sessionId), ["primary", "primary"]);
	assert.deepEqual(app.sessionReports.map((report) => report.body.sequence), [1, 2]);
  } finally {
	await app.dispose();
	app.restore();
  }
});

test("aborts and retries a hung identity report", async () => {
  const app = await fixture("retry-hung-session", {
	controlTimeouts: true,
	sessionResponses: ["hang"],
  });
  try {
	assert.deepEqual(app.sessionReports.map((report) => report.body.sessionId), ["primary"]);
	await app.runTimeout(1000);
	assert.deepEqual(app.sessionAborts, ["primary"]);
	await app.runTimeout(250);
	assert.deepEqual(app.sessionReports.map((report) => report.body.sessionId), ["primary", "primary"]);
	assert.deepEqual(app.sessionReports.map((report) => report.body.sequence), [1, 2]);
  } finally {
	await app.dispose();
	app.restore();
  }
});

test("reports route changes and explicit null without shared activity events", async () => {
  const app = await fixture("route-session");
  try {
	assert.deepEqual(app.intervalDelays, [250]);
	await app.poll();
	assert.equal(app.sessionReports.length, 1);

	app.route.name = "home";
	delete app.route.params;
	await app.poll();
	await app.poll();
	assert.deepEqual(app.sessionReports.map((report) => report.body.sessionId), ["primary", null]);

	app.route.name = "session";
	app.route.params = { sessionID: "next" };
	await app.poll();
	assert.deepEqual(app.sessionReports.map((report) => report.body.sessionId), ["primary", null, "next"]);
  } finally {
	await app.dispose();
	app.restore();
  }
});

test("serializes identity reports and coalesces to the newest pending route", async () => {
  const app = await fixture("coalesced-session", { deferSessionReports: true });
  try {
	assert.equal(app.pendingSessionReportCount, 1);

	app.route.params.sessionID = "intermediate";
	await app.poll();
	app.route.params.sessionID = "newest";
	await app.poll();
	assert.equal(app.sessionReports.length, 1);
	assert.equal(app.pendingSessionReportCount, 1);

	await app.completeSessionReport();
	assert.deepEqual(app.sessionReports.map((report) => report.body.sessionId), ["primary", "newest"]);
	assert.deepEqual(app.sessionReports.map((report) => report.body.sequence), [1, 2]);
	assert.equal(app.sessionReports[1].body.reporterEpoch, app.sessionReports[0].body.reporterEpoch);
	assert.equal(app.pendingSessionReportCount, 1);
	await app.completeSessionReport();
  } finally {
	await app.dispose();
	app.restore();
  }
});

test("keeps the newest pending route when an older identity attempt fails", async () => {
  const app = await fixture("coalesced-failed-session", {
	controlTimeouts: true,
	deferSessionReports: true,
  });
  try {
	app.route.params.sessionID = "intermediate";
	await app.poll();
	app.route.params.sessionID = "newest";
	await app.poll();

	await app.completeSessionReport({ ok: false, status: 503 });
	assert.deepEqual(app.sessionReports.map((report) => report.body.sessionId), ["primary"]);
	await app.runTimeout(250);
	assert.deepEqual(app.sessionReports.map((report) => report.body.sessionId), ["primary", "newest"]);
	assert.deepEqual(app.sessionReports.map((report) => report.body.sequence), [1, 2]);
	await app.completeSessionReport();
  } finally {
	await app.dispose();
	app.restore();
  }
});

test("plugin disposal aborts the identity fetch and discards queued reports", async () => {
  const app = await fixture("disposed-session", {
	controlTimeouts: true,
	sessionResponses: ["hang"],
  });
  try {
	app.route.params.sessionID = "pending";
	await app.poll();
	await app.dispose();

	app.route.params.sessionID = "after-dispose";
	await app.poll();
	assert.deepEqual(app.sessionAborts, ["primary"]);
	assert.deepEqual(app.activeTimeoutDelays, []);
	assert.deepEqual(app.sessionReports.map((report) => report.body.sessionId), ["primary"]);
  } finally {
	app.restore();
  }
});

test("starts identity observation before slow startup setup", async () => {
  const app = await fixture("slow-setup-session", {
	enabled: true,
	ready: false,
	deferPluginStart: true,
  });
  try {
	assert.deepEqual(app.sessionReports.map((report) => report.body.sessionId), ["primary"]);
	assert.deepEqual(app.setupBodies, []);
	app.setReady();
	await app.started;
	assert.equal(app.setupBodies.length, 2);
  } finally {
	app.setReady();
	await app.started;
	await app.dispose();
	app.restore();
  }
});

test("auto-approves permissions only for a yolo agent's session", async () => {
  const app = await fixture("yolo", { yolo: true });
  try {
	app.emit("permission.asked", { id: "root-permission", sessionID: "primary" });
	assert.deepEqual(app.commands, []);
	assert.deepEqual(app.permissionReplies, [{ requestID: "root-permission", reply: "once" }]);
  } finally {
	app.restore();
  }
});

test("does not auto-approve an unrelated agent's permission", async () => {
  const app = await fixture("yolo-unrelated", { yolo: true });
  try {
	app.emit("permission.asked", { id: "other-permission", sessionID: "other" });
	assert.deepEqual(app.permissionReplies, []);
  } finally {
	app.restore();
  }
});

test("auto-approves permissions for a yolo agent's descendants", async () => {
  const app = await fixture("yolo-descendant", { yolo: true });
  try {
	app.emit("session.created", { info: { id: "child", parentID: "primary" } });
	app.emit("permission.asked", { id: "child-permission", sessionID: "child" });
	assert.deepEqual(app.permissionReplies, [{ requestID: "child-permission", reply: "once" }]);
  } finally {
	app.restore();
  }
});

test("a yolo agent waits for questions but not tool permissions", async () => {
  const app = await fixture("yolo-waiting", { yolo: true });
  try {
    app.emit("session.status", {
      sessionID: "primary",
      status: { type: "busy" },
    });
    app.emit("permission.asked", { sessionID: "primary" });
    app.emit("permission.replied", { sessionID: "primary" });
    assert.deepEqual(app.reported, ["idle", "busy"]);

    app.emit("question.asked", { sessionID: "primary" });
    app.emit("question.replied", { sessionID: "primary" });
    assert.deepEqual(app.reported, ["idle", "busy", "waiting", "busy"]);
  } finally {
    app.restore();
  }
});

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
