import assert from "node:assert/strict";
import test from "node:test";
import { TmuxCoderStatus } from "./tui.js";

async function fixture(options = {}) {
  const saved = Object.fromEntries(["TMUX_CODER_AGENT_ID", "TMUX_CODER_PANE_ID", "TMUX_CODER_AGENT_YOLO", "TMUX_CODER_AGENT_SETUP", "TMUX_CODER_AGENT_MODEL", "TMUX_CODER_AGENT_VARIANT"].map((key) => [key, process.env[key]]));
  Object.assign(process.env, { TMUX_CODER_AGENT_ID: "42", TMUX_CODER_PANE_ID: "%7" });
  for (const key of ["TMUX_CODER_AGENT_YOLO", "TMUX_CODER_AGENT_SETUP", "TMUX_CODER_AGENT_MODEL", "TMUX_CODER_AGENT_VARIANT"]) delete process.env[key];
  if (options.yolo) process.env.TMUX_CODER_AGENT_YOLO = "1";
  if (options.setup) process.env.TMUX_CODER_AGENT_SETUP = "1";
  if (options.model) process.env.TMUX_CODER_AGENT_MODEL = options.model;
  if (options.variant) process.env.TMUX_CODER_AGENT_VARIANT = options.variant;
  const oldFetch = globalThis.fetch;
  const oldInterval = globalThis.setInterval;
  const oldClearInterval = globalThis.clearInterval;
  const oldTimeout = globalThis.setTimeout;
  const oldClearTimeout = globalThis.clearTimeout;
  const calls = [];
  const pending = [];
  const timers = new Map();
  const intervals = new Map();
  const handlers = new Map();
  const route = { type: "session", sessionID: "primary" };
  const sessions = new Map();
  const replies = [];
  const navigated = [];
  const models = options.models ?? [];
  globalThis.setTimeout = (fn, delay) => { const token = Symbol(); timers.set(token, { fn, delay }); return token; };
  globalThis.clearTimeout = (token) => timers.delete(token);
  globalThis.setInterval = (fn) => { const token = Symbol(); intervals.set(token, fn); return token; };
  globalThis.clearInterval = (token) => intervals.delete(token);
  globalThis.fetch = (url, input) => {
    const body = JSON.parse(input.body);
    calls.push({ url, body, signal: input.signal });
    if (url.endsWith("/opencode-session") && options.deferIdentity) {
      return new Promise((resolve, reject) => {
        pending.push(resolve);
        input.signal?.addEventListener("abort", () => reject(Error("aborted")), { once: true });
      });
    }
    return Promise.resolve({ ok: true, status: 204 });
  };
  const context = {
    app: { version: "2.0.16" }, location: { directory: "/worktree" },
    data: {
      on(type, fn) { handlers.set(type, fn); return () => handlers.delete(type); },
      session: { get(id) { return sessions.get(id); } },
      location: { default: () => ({ directory: "/worktree" }), model: { async sync() {}, list: () => models } },
    },
    ui: { router: {
      current: () => route,
      navigate(target) { Object.assign(route, target); navigated.push(target); },
    } },
    client: {
      permission: { reply(input) { replies.push(input); return Promise.resolve({ data: true }); } },
      session: {
        async create(input) { calls.push({ create: input }); const data = { id: "created", model: input.model }; sessions.set("created", data); return data; },
        async switchModel(input) { calls.push({ switchModel: input }); sessions.get(input.sessionID).model = input.model; },
        async get(input) { return sessions.get(input.sessionID); },
      },
    },
  };
  const stop = TmuxCoderStatus(context);
  const identity = () => calls.filter((call) => call.url?.endsWith("/opencode-session")).map((call) => call.body);
  const status = () => calls.filter((call) => call.url?.endsWith("/event")).map((call) => call.body.event);
  const emit = (type, data) => handlers.get(type)?.({ type, data });
  const flush = async () => { for (let i = 0; i < 15; i++) await Promise.resolve(); };
  return {
    route, calls, replies, navigated, identity, status, emit, pending, timers, handlers, flush,
    poll() { for (const fn of intervals.values()) fn(); },
    runTimer(delay) { const [key, value] = [...timers].find(([, value]) => value.delay === delay) ?? []; assert.ok(value, `timer ${delay}`); timers.delete(key); value.fn(); },
    restore() {
      stop();
      for (const [key, value] of Object.entries(saved)) { if (value === undefined) delete process.env[key]; else process.env[key] = value; }
      globalThis.fetch = oldFetch;
      globalThis.setInterval = oldInterval;
      globalThis.clearInterval = oldClearInterval;
      globalThis.setTimeout = oldTimeout;
      globalThis.clearTimeout = oldClearTimeout;
    },
  };
}

test("pane-scoped identity reports route switches including null", async () => {
  const app = await fixture();
  try {
    assert.equal(app.identity()[0].sessionId, "primary");
    assert.equal(app.identity()[0].tmuxPaneId, "%7");
    app.route.type = "home"; app.poll(); await app.flush();
    app.route.type = "session"; app.route.sessionID = "next"; app.poll(); await app.flush();
    assert.deepEqual(app.identity().map((item) => item.sessionId), ["primary", null, "next"]);
    assert.deepEqual(app.identity().map((item) => item.sequence), [1, 2, 3]);
  } finally { app.restore(); }
});

test("coalesces queued routes and cleans up subscriptions and pending fetch", async () => {
  const app = await fixture({ deferIdentity: true });
  try {
    app.route.sessionID = "intermediate"; app.poll();
    app.route.sessionID = "newest"; app.poll();
    assert.equal(app.identity().length, 1);
    app.pending.shift()({ ok: true }); await app.flush();
    assert.deepEqual(app.identity().map((item) => item.sessionId), ["primary", "newest"]);
  } finally { app.restore(); }
  assert.equal(app.handlers.size, 0);
  assert.equal(app.timers.size, 0);
});

test("retries failed identity reports with increasing sequence", async () => {
  const app = await fixture({ deferIdentity: true });
  try {
    app.pending.shift()({ ok: false, status: 503 }); await app.flush();
    app.runTimer(250); await app.flush();
    assert.deepEqual(app.identity().map((item) => item.sequence), [1, 2]);
  } finally { app.restore(); }
});

test("tracks overlapping permission and form waits across descendants, ignoring other panes", async () => {
  const app = await fixture();
  try {
    app.emit("session.created", { sessionID: "child", parentID: "primary" });
    app.emit("session.execution.started", { sessionID: "other" });
    app.emit("permission.asked", { sessionID: "other", id: "other-wait" });
    assert.deepEqual(app.status(), ["idle"]);
    app.emit("session.execution.started", { sessionID: "primary" });
    app.emit("permission.asked", { sessionID: "child", id: "one" });
    app.emit("permission.asked", { sessionID: "child", id: "two" });
    app.emit("form.created", { form: { sessionID: "child", id: "form" } });
    app.emit("session.execution.succeeded", { sessionID: "primary" });
    app.emit("permission.replied", { sessionID: "child", id: "one" });
    app.emit("permission.replied", { sessionID: "child", id: "two" });
    assert.equal(app.status().at(-1), "waiting");
    app.emit("form.cancelled", { sessionID: "child", id: "form" });
    assert.deepEqual(app.status(), ["idle", "busy", "waiting", "idle"]);
    app.route.sessionID = "other"; app.poll();
    assert.equal(app.status().at(-1), "idle");
  } finally { app.restore(); }
});

test("yolo replies only to own permissions with v2 session and request IDs; forms remain waiting", async () => {
  const app = await fixture({ yolo: true });
  try {
    app.emit("session.created", { sessionID: "child", parentID: "primary" });
    app.emit("permission.asked", { sessionID: "other", id: "no" });
    app.emit("permission.asked", { sessionID: "child", id: "yes" });
    assert.deepEqual(app.replies, [{ sessionID: "child", requestID: "yes", decision: "once" }]);
    app.emit("form.created", { form: { sessionID: "child", id: "form" } });
    assert.equal(app.status().at(-1), "waiting");
  } finally { app.restore(); }
});

test("v2 startup validates exact model variant and verifies displayed session after handshake", async () => {
  const app = await fixture({ setup: true, model: "anthropic/haiku", variant: "high", models: [
     { providerID: "anthropic", id: "haiku", modelID: "haiku", enabled: true, variants: [{ id: "high" }] },
  ] });
  try {
    await app.flush();
    const posts = app.calls.filter((call) => call.url?.includes("/opencode-setup/"));
    assert.deepEqual(posts.map((item) => item.url.split("/").at(-1)), ["ready", "opened"]);
    assert.deepEqual(posts[1].body, { model: "anthropic/haiku", variant: "high" });
    assert.equal(app.calls.findIndex((item) => item.switchModel) > app.calls.findIndex((item) => item.create), true);
    assert.deepEqual(app.navigated, [{ type: "session", sessionID: "created" }]);
  } finally { app.restore(); }
});

test("startup selects a model alias by its catalog ID rather than its underlying modelID", async () => {
  const app = await fixture({ setup: true, model: "openai/gpt-5.6-luna-fast", variant: "medium", models: [
    { providerID: "openai", id: "gpt-5.6-luna-fast", modelID: "gpt-5.6-luna", enabled: true, variants: [{ id: "medium" }] },
  ] });
  try {
    await app.flush();
    assert.deepEqual(app.calls.find((call) => call.create)?.create.model,
      { providerID: "openai", id: "gpt-5.6-luna-fast", variant: "medium" });
    assert.deepEqual(app.calls.find((call) => call.url?.endsWith("/opened"))?.body,
      { model: "openai/gpt-5.6-luna-fast", variant: "medium" });
  } finally { app.restore(); }
});

test("startup rejects a missing variant before opening a session", async () => {
  const app = await fixture({ setup: true, model: "anthropic/haiku", variant: "missing", models: [
     { providerID: "anthropic", id: "haiku", modelID: "haiku", enabled: true, variants: [{ id: "high" }] },
  ] });
  try {
    await app.flush();
    assert.match(app.calls.find((call) => call.url?.endsWith("/ready")).body.error, /unavailable/);
    assert.equal(app.calls.some((call) => call.create), false);
  } finally { app.restore(); }
});

test("prompt-only startup displays and verifies a target conversation without changing a model", async () => {
  const app = await fixture({ setup: true });
  try {
    await app.flush();
    assert.deepEqual(app.calls.find((call) => call.create).create, { location: { directory: "/worktree" } });
    assert.equal(app.calls.some((call) => call.switchModel), false);
    assert.deepEqual(app.navigated, [{ type: "session", sessionID: "created" }]);
    assert.deepEqual(app.calls.find((call) => call.url?.endsWith("/opened")).body, { model: "", variant: "" });
  } finally { app.restore(); }
});
