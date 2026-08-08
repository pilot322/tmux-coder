"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const dashboard = require("./app.js");

test("management endpoints stay on the same-origin /api prefix", () => {
  assert.equal(dashboard.endpoint("projects"), "/api/projects");
  assert.equal(dashboard.endpoint("sessions/12"), "/api/sessions/12");
  assert.equal(dashboard.endpoint("agents/9/discord-notification"), "/api/agents/9/discord-notification");
});

test("mutation payloads match management API DTOs", () => {
  assert.deepEqual(dashboard.projectPayload({ fullPath: "/src/tmux-coder", title: "Control Room" }), {
    fullPath: "/src/tmux-coder",
    title: "Control Room",
  });

  assert.deepEqual(dashboard.worktreePayload({
    projectId: "7",
    branch: "feature/dashboard",
    origin: "source",
    sourceSessionId: "11",
  }), {
    projectId: 7,
    type: "worktree",
    branch: "feature/dashboard",
    createWorktree: true,
    createBranch: true,
    parentSessionId: 11,
  });

  assert.deepEqual(dashboard.worktreePayload({
    projectId: "7",
    branch: "feature/dashboard",
    origin: "base",
    baseBranch: "origin/main",
  }), {
    projectId: 7,
    type: "worktree",
    branch: "feature/dashboard",
    createWorktree: true,
    createBranch: true,
    baseBranch: "origin/main",
  });

  assert.deepEqual(dashboard.secondaryPayload({
    parentSessionId: "12",
    preferredName: "web",
    relativeWorkingDirectory: "packages/web",
    onDelete: "inherit",
  }), {
    type: "secondary",
    parentSessionId: 12,
    preferredName: "web",
    relativeWorkingDirectory: "packages/web",
    onDelete: "inherit",
  });

  assert.deepEqual(dashboard.agentPayload({
    projectId: "7",
    sessionId: "12",
    kind: "opencode",
    displayName: "frontend",
    model: "anthropic/claude-sonnet-4",
    variant: "high",
    prompt: "Inspect the UI",
    yolo: true,
  }), {
    projectId: 7,
    sessionId: 12,
    kind: "opencode",
    displayName: "frontend",
    model: "anthropic/claude-sonnet-4",
    variant: "high",
    prompt: "Inspect the UI",
    yolo: true,
  });
});

test("Base-ref Worktree mode rejects a blank explicit base ref", () => {
  assert.throws(() => dashboard.worktreePayload({
    projectId: "7",
    branch: "feature/dashboard",
    origin: "base",
    baseBranch: "   ",
  }), /explicit base ref/i);
});

test("worktree conflict retries switch only to valid creation modes", () => {
  const fresh = dashboard.worktreePayload({
    projectId: "7",
    branch: "feature/dashboard",
    origin: "base",
    baseBranch: "main",
  });

  assert.deepEqual(dashboard.worktreeRetryPayload(fresh, "branch_exists"), {
    projectId: 7,
    type: "worktree",
    branch: "feature/dashboard",
    createWorktree: true,
    createBranch: false,
  });
  assert.deepEqual(dashboard.worktreeRetryPayload(fresh, "worktree_exists"), {
    projectId: 7,
    type: "worktree",
    branch: "feature/dashboard",
    createWorktree: false,
    createBranch: false,
  });
  assert.equal(dashboard.worktreeRetryPayload(fresh, "path_blocked"), null);
});

test("Session deletion requests force Worktrees but preserve Secondary policy", () => {
  const worktree = dashboard.sessionDeleteRequest({ id: 12, type: "worktree", sessionName: "feature-ui" });
  assert.equal(worktree.resource, "sessions/12?force=true");
  assert.match(worktree.title, /Worktree/);
  assert.match(worktree.message, /removes its Git worktree directory/i);
  assert.match(worktree.message, /uncommitted changes/i);

  const cascade = dashboard.sessionDeleteRequest({ id: 13, type: "secondary", sessionName: "web", onDelete: "cascade" });
  assert.equal(cascade.resource, "sessions/13");
  assert.match(cascade.message, /cascade policy/i);
  assert.match(cascade.message, /descendant Secondary Sessions/i);

  const inherit = dashboard.sessionDeleteRequest({ id: 14, type: "secondary", sessionName: "tests", onDelete: "inherit" });
  assert.equal(inherit.resource, "sessions/14");
  assert.match(inherit.message, /inherit policy/i);
  assert.match(inherit.message, /reparents its direct Secondary children/i);
});

test("pending decisions are identified by all request-defining form inputs", () => {
  const project = { fullPath: "/src/one", title: "One" };
  assert.equal(dashboard.projectRequestKey(project), dashboard.projectRequestKey({ fullPath: " /src/one ", title: " One " }));
  assert.notEqual(dashboard.projectRequestKey(project), dashboard.projectRequestKey({ fullPath: "/src/two", title: "One" }));
  assert.notEqual(dashboard.projectRequestKey(project), dashboard.projectRequestKey({ fullPath: "/src/one", title: "Two" }));

  const worktree = {
    projectId: "7",
    branch: "feature/ui",
    origin: "source",
    sourceSessionId: "11",
  };
  assert.equal(dashboard.worktreeRequestKey(worktree), dashboard.worktreeRequestKey(Object.assign({}, worktree)));
  assert.notEqual(dashboard.worktreeRequestKey(worktree), dashboard.worktreeRequestKey(Object.assign({}, worktree, { branch: "feature/api" })));
  assert.notEqual(dashboard.worktreeRequestKey(worktree), dashboard.worktreeRequestKey({
    projectId: "7",
    branch: "feature/ui",
    origin: "base",
    baseBranch: "main",
  }));
});

test("resource equality and action identity support stable polling renders", () => {
  const projects = [{ id: 1, title: "One", fullPath: "/src/one" }];
  assert.equal(dashboard.resourceDataEqual(projects, [{ id: 1, title: "One", fullPath: "/src/one" }]), true);
  assert.equal(dashboard.resourceDataEqual(projects, [{ id: 1, title: "Two", fullPath: "/src/one" }]), false);

  const before = { action: "delete-session", sessionId: "12" };
  const replacement = { action: "delete-session", sessionId: "12" };
  assert.equal(dashboard.actionFocusKey(before), dashboard.actionFocusKey(replacement));
  assert.notEqual(dashboard.actionFocusKey(before), dashboard.actionFocusKey({ action: "delete-session", sessionId: "13" }));
  assert.notEqual(dashboard.actionFocusKey(before), dashboard.actionFocusKey({ action: "new-agent", sessionId: "12" }));
});

test("TC Agent creation is available only when a Session exists", () => {
  assert.equal(dashboard.canCreateAgent([]), false);
  assert.equal(dashboard.canCreateAgent([{ id: 1, type: "main" }]), true);
});

test("session topology preserves Worktree provenance and Secondary nesting", () => {
  const sessions = [
    { id: 5, projectId: 2, type: "secondary", parentSessionId: 4, sessionName: "tests" },
    { id: 3, projectId: 2, type: "worktree", parentSessionId: -1, sessionName: "feature" },
    { id: 4, projectId: 2, type: "secondary", parentSessionId: 3, sessionName: "web" },
    { id: 1, projectId: 2, type: "main", parentSessionId: -1, sessionName: "main" },
    { id: 6, projectId: 2, type: "worktree", parentSessionId: 3, sessionName: "review" },
    { id: 9, projectId: 8, type: "main", parentSessionId: -1, sessionName: "other" },
  ];

  const roots = dashboard.buildTopology(2, sessions);
  assert.deepEqual(roots.map((node) => node.session.id), [1, 3]);
  assert.deepEqual(roots[1].children.map((node) => node.session.id), [6, 4]);
  assert.deepEqual(roots[1].children[1].children.map((node) => node.session.id), [5]);
});

test("OpenCode links are exposed only from the exact API field", () => {
  assert.equal(dashboard.openCodeWebURL({ kind: "opencode" }), "");
  assert.equal(dashboard.openCodeWebURL({ kind: "opencode", openCodeSessionId: "ses_123" }), "");
  assert.equal(
    dashboard.openCodeWebURL({ kind: "opencode", openCodeWebUrl: "https://tc.tailnet.ts.net/session/ses_123" }),
    "https://tc.tailnet.ts.net/session/ses_123",
  );
  assert.equal(dashboard.openCodeWebURL({ kind: "claude", openCodeWebUrl: "https://wrong.example" }), "");
  assert.equal(dashboard.openCodeWebURL({ kind: "opencode", openCodeWebUrl: "javascript:alert(1)" }), "");
});

test("resource responses require the expected array", () => {
  const projects = [{ id: 1, title: "Control Room" }];
  assert.equal(dashboard.resourceItems("projects", { projects }), projects);
  assert.throws(
    () => dashboard.resourceItems("sessions", { error: "proxy returned HTML" }),
    /malformed sessions response: expected sessions to be an array/i,
  );
  assert.throws(
    () => dashboard.resourceItems("agents", { agents: {} }),
    /malformed agents response: expected agents to be an array/i,
  );
});

test("malformed resource responses fail refresh without committing empty data", async () => {
  const bodies = {
    projects: { projects: [{ id: 1 }] },
    sessions: { error: "proxy returned HTML" },
    agents: { agents: {} },
  };
  const commits = [];
  const errors = [];
  const coordinator = dashboard.createRefreshCoordinator(
    async (resource) => dashboard.resourceItems(resource, bodies[resource]),
    (resource, value) => commits.push([resource, value]),
    (resource, error) => errors.push([resource, error.message]),
  );

  await coordinator.refresh();

  assert.deepEqual(commits, [["projects", [{ id: 1 }]]]);
  assert.deepEqual(errors, [
    ["sessions", "Malformed sessions response: expected sessions to be an array"],
    ["agents", "Malformed agents response: expected agents to be an array"],
  ]);
});

test("slow refreshes coalesce overlaps and queue exactly one follow-up", async () => {
  const pending = [];
  const commits = [];
  const errors = [];
  const coordinator = dashboard.createRefreshCoordinator(
    (resource) => new Promise((resolve, reject) => pending.push({ resource, resolve, reject })),
    (resource, value) => commits.push([resource, value]),
    (resource, error) => errors.push([resource, error.message]),
  );

  const first = coordinator.refresh();
  const second = coordinator.refresh();
  const third = coordinator.refresh();
  assert.equal(pending.length, 3);
  assert.equal(first, second);
  assert.equal(second, third);

  pending[0].resolve(["first-project"]);
  pending[1].resolve(["first-session"]);
  pending[2].resolve(["first-agent"]);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(pending.length, 6);

  pending[3].resolve(["next-project"]);
  pending[4].resolve(["next-session"]);
  pending[5].reject(new Error("agents unavailable"));
  await Promise.all([first, second, third]);

  assert.deepEqual(commits, [
    ["projects", ["first-project"]],
    ["sessions", ["first-session"]],
    ["agents", ["first-agent"]],
    ["projects", ["next-project"]],
    ["sessions", ["next-session"]],
  ]);
  assert.deepEqual(errors, [["agents", "agents unavailable"]]);
  assert.equal(dashboard.REFRESH_INTERVAL_MS, 1000);
});

test("mutation invalidation suppresses an in-flight stale refresh", async () => {
  const pending = [];
  const commits = [];
  const errors = [];
  const coordinator = dashboard.createRefreshCoordinator(
    (resource) => new Promise((resolve, reject) => pending.push({ resource, resolve, reject })),
    (resource, value) => commits.push([resource, value]),
    (resource, error) => errors.push([resource, error.message]),
  );

  const refresh = coordinator.refresh();
  coordinator.invalidate();
  coordinator.refresh();
  pending[0].resolve(["stale-project"]);
  pending[1].resolve(["stale-session"]);
  pending[2].reject(new Error("stale error"));
  await new Promise((resolve) => setImmediate(resolve));

  pending[3].resolve(["current-project"]);
  pending[4].resolve(["current-session"]);
  pending[5].reject(new Error("agents unavailable"));
  await refresh;

  assert.deepEqual(commits, [
    ["projects", ["current-project"]],
    ["sessions", ["current-session"]],
  ]);
  assert.deepEqual(errors, [["agents", "agents unavailable"]]);
});
