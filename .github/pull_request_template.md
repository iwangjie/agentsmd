## What changed

<!-- One paragraph. What behaviour is different after this pull request? -->

## Why

<!-- The problem this solves. Link the issue if there is one. -->

## Checks

- [ ] `gofmt -l .` reports no files
- [ ] `go vet ./...` passes
- [ ] `go test ./internal/... -race` passes
- [ ] `bun run typecheck` passes
- [ ] `bun run build` produces `build/darwin-arm64/AgentsMD.app`
- [ ] `bun run generate` leaves `src/mygo.ts` unchanged, if a service method changed

## The rules

- [ ] The source file `AGENTS.raw.md` is never rewritten by the app
- [ ] A failed request reports the reason instead of switching models
- [ ] Background rewrites do not repaint the editor or move the cursor
