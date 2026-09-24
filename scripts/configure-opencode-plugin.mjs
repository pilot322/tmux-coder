import { existsSync, readFileSync, renameSync, unlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [configDir, pluginDir] = process.argv.slice(2);
if (!configDir || !pluginDir) throw Error("usage: configure-opencode-plugin.mjs <config-dir> <plugin-dir>");
const plugin = pathToFileURL(pluginDir).href;

// V1 installed a server plugin in the auto-discovery directory. It cannot
// load in V2, even if the new CLI-only plugin is configured correctly.
const legacyPath = join(configDir, "plugin", "tmux-coder.js");
const legacyBackup = join(configDir, "tmux-coder.v1.js.backup");
if (existsSync(legacyPath)) {
  const source = readFileSync(legacyPath, "utf8");
  if (source.includes("// tmux-coder OpenCode plugin.") && source.includes("export const TmuxCoderStatus") && !source.includes("export default")) {
    if (!existsSync(legacyBackup)) renameSync(legacyPath, legacyBackup);
    else if (readFileSync(legacyBackup, "utf8") === source) unlinkSync(legacyPath);
    else throw Error(`${legacyBackup} already exists with different contents; move ${legacyPath} outside plugin discovery manually`);
  }
}

function read(path) {
  try { return JSON.parse(readFileSync(path, "utf8")); }
  catch (error) { if (error.code === "ENOENT") return {}; throw error; }
}
function save(path, value) {
  const temporary = `${path}.tmp`;
  writeFileSync(temporary, JSON.stringify(value, null, 2) + "\n");
  renameSync(temporary, path);
}

const cliPath = join(configDir, "cli.json");
const cli = read(cliPath);
if (cli.plugins !== undefined && !Array.isArray(cli.plugins)) throw Error(`${cliPath}: plugins must be an array`);
cli.plugins = [...(cli.plugins ?? []).filter((entry) => entry !== plugin), plugin];
save(cliPath, cli);

const tuiPath = join(configDir, "tui.json");
const tui = read(tuiPath);
if (tui.plugin !== undefined) {
  if (!Array.isArray(tui.plugin)) throw Error(`${tuiPath}: plugin must be an array`);
  const filtered = tui.plugin.filter((entry) => entry !== plugin);
  if (filtered.length !== tui.plugin.length) {
    if (filtered.length) tui.plugin = filtered;
    else delete tui.plugin;
    save(tuiPath, tui);
  }
}
