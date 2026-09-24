import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { execFileSync } from "node:child_process";
import test from "node:test";

test("v2 plugin migration preserves other plugins and settings on repeat installs", () => {
  const dir = mkdtempSync(join(tmpdir(), "tmux-coder-plugin-install-"));
  try {
    const plugin = pathToFileURL(join(dir, "plugin", "tmux-coder")).href;
    writeFileSync(join(dir, "tui.json"), JSON.stringify({ theme: "dark", plugin: ["other", plugin] }));
    writeFileSync(join(dir, "cli.json"), JSON.stringify({ foo: true, plugins: ["other", plugin] }));
    const script = new URL("./configure-opencode-plugin.mjs", import.meta.url);
    for (let i = 0; i < 2; i++) execFileSync(process.execPath, [fileURLToPath(script), dir, join(dir, "plugin", "tmux-coder")]);
    assert.deepEqual(JSON.parse(readFileSync(join(dir, "tui.json"))), { theme: "dark", plugin: ["other"] });
    assert.deepEqual(JSON.parse(readFileSync(join(dir, "cli.json"))), { foo: true, plugins: ["other", plugin] });
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("v2 plugin migration preserves the legacy v1 server plugin outside discovery", () => {
  const dir = mkdtempSync(join(tmpdir(), "tmux-coder-plugin-install-"));
  try {
    mkdirSync(join(dir, "plugin"));
    const legacy = join(dir, "plugin", "tmux-coder.js");
    const backup = join(dir, "tmux-coder.v1.js.backup");
    const source = '// tmux-coder OpenCode plugin.\nexport const TmuxCoderStatus = async () => ({});\n';
    writeFileSync(legacy, source);
    const script = new URL("./configure-opencode-plugin.mjs", import.meta.url);
    execFileSync(process.execPath, [fileURLToPath(script), dir, join(dir, "plugin", "tmux-coder")]);
    assert.equal(existsSync(legacy), false);
    assert.equal(readFileSync(backup, "utf8"), source);
    execFileSync(process.execPath, [fileURLToPath(script), dir, join(dir, "plugin", "tmux-coder")]);
    assert.equal(readFileSync(backup, "utf8"), source);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("v2 plugin migration leaves unrelated or changed legacy files untouched", () => {
  const dir = mkdtempSync(join(tmpdir(), "tmux-coder-plugin-install-"));
  try {
    mkdirSync(join(dir, "plugin"));
    const legacy = join(dir, "plugin", "tmux-coder.js");
    writeFileSync(legacy, "export default {};\n");
    const script = new URL("./configure-opencode-plugin.mjs", import.meta.url);
    execFileSync(process.execPath, [fileURLToPath(script), dir, join(dir, "plugin", "tmux-coder")]);
    assert.equal(readFileSync(legacy, "utf8"), "export default {};\n");
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("v2 plugin migration refuses to overwrite a different legacy backup", () => {
  const dir = mkdtempSync(join(tmpdir(), "tmux-coder-plugin-install-"));
  try {
    mkdirSync(join(dir, "plugin"));
    const legacy = join(dir, "plugin", "tmux-coder.js");
    const backup = join(dir, "tmux-coder.v1.js.backup");
    const source = '// tmux-coder OpenCode plugin.\nexport const TmuxCoderStatus = async () => ({});\n';
    writeFileSync(legacy, source);
    writeFileSync(backup, "previous backup");
    const script = new URL("./configure-opencode-plugin.mjs", import.meta.url);
    assert.throws(() => execFileSync(process.execPath, [fileURLToPath(script), dir, join(dir, "plugin", "tmux-coder")], { stdio: "pipe" }));
    assert.equal(readFileSync(legacy, "utf8"), source);
    assert.equal(readFileSync(backup, "utf8"), "previous backup");
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
