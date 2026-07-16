## Agent skills

### Issue tracker

Issues and PRDs are tracked as GitHub issues using the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default label vocabulary (needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout — one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.

### Testing

`go test ./...` for unit tests; build + drive a real isolated instance for end-to-end. **Never run `./dev kill -a`.** See `docs/agents/testing.md`.
