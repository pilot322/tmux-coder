# ADR-0006: Secondary Session Hierarchy and Lifecycle

## Status

Accepted

## Context

Secondary Sessions are tmux sessions that live under an existing Session rather than directly under a Project. They need enough structure to support a navigable TUI tree, safe creation paths, unique tmux targets, and predictable deletion behavior.

## Decision

Secondary Sessions may be parented by Main Sessions, Worktree Sessions, or other Secondary Sessions. The maximum ancestry depth is five Sessions total, counting Main or Worktree roots as depth one.

Secondary creation uses `parentSessionId` as the authoritative parent and Project source. A supplied `projectId` must match the parent Session's Project.

`relativeWorkingDirectory` is stored normalized: clean path, no trailing slash, `.` as empty, no absolute paths, and no escaping paths. It is resolved relative to the containing root: Project path for Main-rooted trees and Worktree path for Worktree-rooted trees. Non-empty directories must exist. Empty directories are valid only with `preferredName`.

Secondary Session names shown in the CLI are derived from `preferredName` when present, otherwise from the basename of `relativeWorkingDirectory`. Names are globally suffixed with `-2`, `-3`, etc. on collision.

Secondary tmux session names are separate from CLI-facing names. They use the Project directory name plus an underscore prefix, followed by the derived Secondary Session name with tmux-safe separators, for example `api_pkg` for CLI session `pkg` under Project `api`.

Deletion uses the stored `onDelete` policy. `cascade` deletes the selected Secondary and all descendant Secondary Sessions. `inherit` deletes the selected Secondary and reparents its direct Secondary children to the deleted Session's parent, preserving their descendants. Project and Worktree deletion cascade all descendant Secondary Sessions regardless of their own policy.

## Consequences

The API returns `parentSessionId`, `relativeWorkingDirectory`, and `onDelete` for Secondary Sessions while preserving the existing `parent` field.

The TUI can build a flat API response into a tree per Project without a nested API response shape.

`inherit` requires repository support for updating a Session parent without changing its ID.
