---
name: tmxc-handoff
description: Hand off the current work to a new tmux-coder agent in the same session.
disable-model-invocation: true
metadata:
  opencode/autoinvoke: false
---

# TC handoff

1. Invoke the `handoff` skill and follow it to write a handoff document. Keep its absolute path; check that the file exists and is readable. Treat any arguments to this skill as the next agent's focus and pass them to `handoff`.
2. Identify the current **tmux-coder Session ID**, not an OpenCode conversation ID or a tmux pane ID. Use `TMUX_CODER_SESSION_ID` if available in the execution environment. If it is missing, resolve the current managed session from tmux-coder's session list (matching the current tmux session name), or ask the user for the Session ID if you cannot establish it unambiguously. Confirm the handoff path and Session ID before creating an agent.
3. Create a fresh OpenCode TC Agent with an initial prompt pointing to the handoff document:

   ```sh
   tmux-coder new opencode --session-id "$session_id" --prompt "Read $handoff_path and continue the work described there. Follow its suggested skills where relevant."
   ```

   Use the tmux-coder executable connected to the same Daemon as the current TC Agent. Pass `--session-id` explicitly: without it, `new` may reuse the caller's pane instead of opening a new agent window. Omit `--pane`.
4. Report the created agent ID and handoff path. If creation fails, report the error and the handoff path so the user can retry; keep the current agent and handoff document intact.
