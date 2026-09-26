---
name: tmxc-handoff
description: Hand off the current work to a new tmux-coder agent in the same session.
disable-model-invocation: true
metadata:
  opencode/autoinvoke: false
---

# TC handoff

1. Invoke the `handoff` skill and follow it to write a handoff document. Keep its absolute path; check that the file exists and is readable. Treat any arguments to this skill as the next agent's focus and pass them to `handoff`.
2. Resolve the current **tmux-coder Session ID** from this conversation, even if shell tools have no `TMUX` or `TMUX_CODER_SESSION_ID`. Use the current OpenCode conversation ID supplied by the agent host (a `ses_...` ID in the session context). Query the tmux-coder Daemon's `GET /agents` and find the **one** OpenCode TC Agent whose `openCodeSessionId` equals that ID. Its `sessionId` is the target TC Session ID:

   ```sh
   conversation_id='ses_...' # replace with this conversation's ID from agent context
   daemon_url="http://127.0.0.1:${TMUX_CODERD_PORT:-64357}"
   session_id=$(curl -fsS "$daemon_url/agents" | jq -er --arg id "$conversation_id" '
     [.agents[] | select(.kind == "opencode" and .openCodeSessionId == $id)] |
     if length == 1 then .[0].sessionId else error("expected exactly one matching TC Agent; found \(length)") end
   ')
   ```

   Use the current TC Daemon's address if it differs from the default (for example, in a development build). Match by exact conversation ID, not by working directory or project: several Sessions can share both. If no unique match is available, stop and report what failed rather than guessing a Session ID. Confirm the handoff path and Session ID before creating an agent.
3. Create a fresh OpenCode TC Agent with an initial prompt pointing to the handoff document:

   ```sh
   tmux-coder new opencode --session-id "$session_id" --prompt "Read $handoff_path and continue the work described there. Follow its suggested skills where relevant."
   ```

   Use the tmux-coder executable connected to the same Daemon as the lookup (and the matching `TMUX_CODERD_PORT` if needed). Pass `--session-id` explicitly: without it, `new` may reuse the caller's pane instead of opening a new agent window. Omit `--pane`.
4. Report the created agent ID and handoff path. If creation fails, report the error and the handoff path so the user can retry; keep the current agent and handoff document intact.
