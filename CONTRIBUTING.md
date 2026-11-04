# Contributing

Thanks for taking the time to improve AgentsMD.

## Before you start

- For a bug, open an issue with the steps that reproduce it and the macOS
  version you run.
- For a feature, open an issue first so we can agree on the shape before you
  write the code. Small fixes can go straight to a pull request.

## Setup

Prerequisites: Go 1.27+, [Bun](https://bun.sh), and Xcode command line tools.

```bash
bun install
bun run dev
```

## Checks

Run these before opening a pull request; CI runs the same commands:

```bash
gofmt -l .
go vet ./...
go test ./internal/... -race
bun run typecheck
bun run build
```

If you changed a Go service method, regenerate the bindings and commit the
result:

```bash
bun run generate
```

## Rules the code depends on

- **The source file is sacred.** `AGENTS.raw.md` is written only by the user.
  Nothing in the app may rewrite it, reformat it or repair it in place.
- **Never pick a model on failure.** Report the reason and let the author
  choose; do not fall back to another model.
- **Typing is never interrupted.** A background rewrite must not repaint the
  editor, move the cursor or break an in-progress composition.
- **Writes are atomic.** Write to a temp file, sync, rename, and verify the
  read-back. Back up before replacing `AGENTS.md`.

## Commit messages

Short imperative subject, then a paragraph on why the change is needed when it
is not obvious. Keep unrelated changes in separate commits.

## License

By contributing, you agree that your work is released under the
[MIT License](LICENSE).
