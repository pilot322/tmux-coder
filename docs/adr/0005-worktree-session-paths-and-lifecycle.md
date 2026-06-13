# Worktree Session paths and lifecycle

Worktree Sessions use a sibling-derived external worktree path and an attachable tmux session name derived from the Project path basename plus a sanitized branch slug, with readable numeric suffixes on collision. We chose this over a central worktree root or project-id-heavy names because the resulting directories and tmux sessions stay inspectable while avoiding worktrees nested inside the Project directory.

Deleting a Worktree Session removes its git worktree, tmux session, and Session record, but deleting a Project only removes tmux-coder's ownership of its Sessions and intentionally leaves external git worktree directories on disk. This keeps Project deletion from becoming a destructive filesystem operation with hidden dirty-worktree semantics; users delete Worktree Sessions explicitly when they want the worktree removed.
