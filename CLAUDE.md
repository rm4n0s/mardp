# CLAUDE.md

Guidance for Claude Code when working in this repository (Go, hosted on GitHub).

## Project
mardp is an RDP host for macOS written in Go, so that RDP clients such as Remmina on Linux can connect to a Mac: they see its desktop, control it, and hear its sound, well enough to watch video. It is a desktop app whose user interface is built on [Glutter](https://github.com/rm4n0s/glutter), and it contains its own RDP server library (`rdp/`), written from scratch.

**The specification comes first** (the user, 2026-11-18: "before you start implementation, we will write the specification first"). `docs/SPEC.md` and `docs/IMPLEMENTATION.md` exist; work on the steps starts only when the user says so (rule 1 of `docs/IMPLEMENTATION.md`). Until then, "Working through the steps" below does not apply.

**`docs/SPEC.md` is the source of truth.** Read the sections that cover a task before starting it. If the code and the spec disagree, or a task needs a decision the spec doesn't make, ask instead of guessing. When a decision changes, update the spec on the same branch.

## Commands
- Build: `go build ./...`
- Test all: `go test -race -tags debug_mode ./...` (a debug build of Glutter, which the UI tests need) and `go test ./...` (a build without tags is a release build)
- Test one package: `go test ./internal/foo -run TestName -v`
- The library alone, as on Linux: `GOOS=linux go build ./rdp/...`
- Vet: `go vet ./...`
- Lint: `golangci-lint run`
- Format: `gofmt -w .`
- Generate: `go generate ./...`
- After changing dependencies: `go mod tidy`
- On macOS run the tests with `TMPDIR=$(cd "$TMPDIR" && pwd -P)/ go test ...` (the temporary folder is under `/var`, a link to `/private/var`, and tests compare paths that the program resolves)

## Definition of done
Before reporting a task as finished, run `gofmt`, `go vet ./...`, `golangci-lint run`, `go test -race -tags debug_mode ./...` and `go test ./...`, and fix anything they report. Say so explicitly if any step was skipped or failed. Also:
- Every error Tag has a test that asserts its route
- Every new exported identifier has a doc comment

## Layout
- `cmd/mardp/` is the app's `main` package; keep `main.go` thin
- `rdp/` is the RDP server library. It imports only the standard library and `github.com/rm4n0s/errors`: no cgo, no Glutter, nothing from `internal/`, so it builds and is tested on Linux too
- `internal/host` is one session's pipeline, written against interfaces and tested with fakes; `internal/mac/...` holds the macOS code (capture, encoders, input), the only place `import "C"` may appear; `internal/ui` is the Glutter app, the only package that imports Glutter
- `docs/SPEC.md` is the specification; `docs/IMPLEMENTATION.md` is the step-by-step plan; `docs/IMPLEMENTED.md` logs finished steps
- `scripts/` holds helper scripts
- Tests live next to the code (`foo_test.go`)
- The exact package list is defined in `docs/SPEC.md`

## Go conventions
- Never ignore a returned error; handle it or return it
- Pass `context.Context` as the first parameter of anything that does I/O (files, processes, network). Name it `ctx`. Name a Glutter `BuildContext` `bc`
- Tests are table-driven
- No global mutable state and no singletons. Package-level read-only values are fine and are never reassigned
- Prefer the standard library. Allowed dependencies: `github.com/rm4n0s/errors`, `github.com/rm4n0s/glutter` and what Glutter brings in. Ask before adding any other
- cgo only in the packages under `internal/mac` (SPEC D14); every such package has a stub for other systems
- Only Glutter's UI thread touches widgets and render objects. Other goroutines hand work over through `SetState` or the binding's `Post`
- Long-running work never blocks the UI thread, and takes a `ctx` so it can be cancelled

## Glutter
- Follow Glutter's API conventions: `NewX(required..., opts ...XOpts) *X` constructors, options structs, `foundation.Opt[T]` for nullable options
- When Glutter is missing something or has a bug, work around it in mardp without stopping, and mark the workaround with a `// Glutter:` comment that says what it works around and what Glutter should provide instead
- Keep a list of these workarounds in `docs/GLUTTER-ISSUES.md` (what is missing or broken, where the workaround is, and what the fix in Glutter would be), and mention new entries in the report at the end of the task. Create the file with the first entry; remove an entry when the fix is in the Glutter version mardp uses
- Read Glutter's source in the Go module cache when its behavior is unclear; its `docs/SPEC.md` describes its design

## Error handling
Use `github.com/rm4n0s/errors` for all errors. Never use `fmt.Errorf`, and never import the standard library `errors` package: this package re-exports `errors.Is`, `errors.As` and `errors.Unwrap`, so a single import covers everything. Read `AI_GUIDE.md` of github.com/rm4n0s/errors (in the Go module cache: `go list -m -f '{{.Dir}}' github.com/rm4n0s/errors`) before writing error code.

**Creating errors**
- New failure with a fixed message: `errors.New("Tag", "message", "key", value, ...)` (all metadata in this one call, never `New(...).SetMetadata(...)`)
  - Tag: short PascalCase name of the failure, e.g. `FileOpenFailed`
  - Trailing arguments are key/value metadata pairs, e.g. `"path", path`
- New failure with a formatted message: `errors.NewErrf("Tag", "format %s", arg)`
- Wrapping an error that is NOT already an `*errors.Error` (stdlib, third-party): `errors.NewErr("Tag", err)`. Add metadata with one `.SetMetadata("key", value, "key2", value2)` call (a later call merges into the metadata, it does not replace it, but use one call unless the data only becomes available later)
- Every `New`, `NewErr` and `NewErrf` call captures the stack at that point
- Construct the error in the function that detects the failure, never in a helper that only builds it (`errNoWindow()`, `fail(tag, msg)`): the route is the stack at the constructor, so a helper puts its own name in every route. The tag is a string literal at the call
- Sentinels are struct literals, `&errors.Error{Tag: "X"}`, never a package-level `errors.New`: its stack is `init`, so every route through it is wrong

**Propagating errors**
- If the error already came from this package, return it untouched: `return err`. Re-wrapping it would replace the Tag and the stack and break route tests
- Functions return the plain `error` interface, never `*errors.Error` (avoids the non-nil-interface-holding-nil-pointer bug)
- `errors.FromError(err)` is a plain type assertion. It fails if something else wrapped the error, so don't wrap these errors with anything

**Inspecting errors**
- `appErr, ok := errors.FromError(err)`, then use `appErr.Tag`, `appErr.Metadata`, `appErr.Message`
- Use `errors.Is` / `errors.As` / `errors.Unwrap` for the underlying cause stored in `OriginalErr`

**Testing errors**
- Every new failure case gets its own unique Tag and a test that asserts the route, not just that an error occurred:
  ```go
  appErr, ok := errors.FromError(err)
  if !ok {
      t.Fatalf("expected *errors.Error, got %T", err)
  }
  if !appErr.HasRoute("Server.Accept->Handshake.ReadConnectionRequest.ConnectionRequestInvalid") {
      t.Errorf("unexpected failure path: %s", appErr.Route())
  }
  ```
- A route lists the call chain from outermost to innermost (`Type.Method` or `Func`, joined by `->`) and ends with the Tag. `HasRoute` matches by substring
- Log errors with `slog`, passing the error as an attribute (`l.Error("failed", "err", err)`): `*errors.Error` is a `slog.LogValuer`, so the log gets the Tag, Route, Metadata and stack frames, and the logged object can be replayed in a test via `errors.ErrorJson` and `errors.FromJson`. Do not call `ToJson` or `json.Marshal` on an error yourself

## Git and GitHub
- Never commit or push directly to `main`; work on a branch named `feat/...` or `fix/...`
- **Exception:** if `docs/IMPLEMENTATION.md` exists, follow its Rules section for the steps it lists. Each step is done on its own branch (`step/<id>`), one at a time. A finished step (all Definition-of-done checks passing, its entry added to `docs/IMPLEMENTED.md` on that branch) is fast-forward merged into the local `main`, without a PR, and tagged `step-<id>` locally. **GitHub gets milestones, not steps:** `main` and the tags are pushed once, when the last step of a milestone (a `## M<n>` section of `docs/IMPLEMENTATION.md`) is merged. Never commit work for a step directly on `main`. Everything not covered by that file (other changes, fixes outside the listed steps) still follows the rule above
- **Every time `main` is pushed to GitHub, also tag and push a new version** (`vMAJOR.MINOR.PATCH`): ask the user for the number first (never guess; a short multiple-choice question with a recommended default is fine), then `git tag -a vX.Y.Z -m ...` on the commit pushed and push the tag with it. This holds for every publish, not only a milestone's, until the user says otherwise (the user, 2026-11-05: "when publish add a version as well")
- Commit messages: imperative mood, e.g. "Add retry to client"
- PRs: short description, link the issue (`Closes #123`), keep them small
- No `.github/` directory and no GitHub Actions workflows: never add them. The Definition-of-done checks are run locally

## Working through the steps
- **Never stop until every step in `docs/IMPLEMENTATION.md` is implemented** (through the last one in the file; as the plan grows, the last step moves). A finished step is never the end of a turn: the turn's last action is a tool call that works on the next step, not a summary. Do not write "starting the next step now" and then end the turn; start it. Report a finished step (and a published milestone) in one or two lines in the middle of the work, and keep working
- **One agent only** (the user, 2026-11-05: "only one agent will work on the implementation and that will be you"): the session works on the steps itself, one at a time; it does not hand work to subagents
- Don't stop to ask for confirmation, and don't end a turn with a question such as "Should I start the next step?", until every step in `docs/IMPLEMENTATION.md` is finished (through the last one). After a step is merged (or a milestone published), report it briefly and start the next step in the same turn
- The only reasons to stop are the ones rule 9 of `docs/IMPLEMENTATION.md` names (an ambiguous step, a wrong spec, a missing tool, a new dependency, `sudo`, a download, a macOS permission prompt, a check that keeps failing, a step that says **ask the user**) and a push of a milestone that keeps failing (rule 14). Then stop, ask, and continue with the same step after the answer
- **Manual checks** don't stop the work: add them to `docs/MANUAL-CHECKS.md` (create it if needed) with the step id and the exact command for the user, write "pending, see docs/MANUAL-CHECKS.md" in the step's `IMPLEMENTED.md` entry, and go on. When the user answers, record the answer in that entry and remove the item from the list (delete the file when it is empty)
- A request such as "publish to main" that is already satisfied gets a one-line answer, then work continues with the next step

## Don't
- Don't edit generated files (`*_gen.go`); regenerate them with `go generate ./...`
- Don't commit secrets or `.env` files
- Don't leave processes running: every server or client started for a test, a proof or a check is stopped when it is no longer needed (run it under a timeout, in its own process group), and `pgrep -fl` shows nothing left before a task is reported done
- Never look for, list, open or read files outside this project folder (no home-directory databases, configs, logs or other projects). If something outside it is needed, ask the user to provide it. Exceptions: dependency source in the Go module cache, and temporary folders your own tests and scripts create

## Testing notes
- Never run Go fuzzing (`-fuzz`) without `-parallel=1`, a short `-fuzztime` (30s at most) and a memory limit (`GOMEMLIMIT=2GiB`); each fuzz worker grows its own memory. Prefer the seed corpus and seeded random tests
- Run the tests of one package while working; run `./...` only for the final checks
- Tests against a real RDP client run FreeRDP in a Linux virtual machine on this Mac, in UTM (SPEC §15.3, D16), driven through `scripts/vm.sh` (UTM's `utmctl`), with the build tag `mardp_freerdp`; they are not part of the Definition-of-done checks. **Never ask for or use SSH access to the user's Linux machine** (the user, 2026-11-19: "I am not going to give you ssh access to a linux server"). The virtual machine holds nothing of the user's. A test run leaves it as it found it: a machine the run started is stopped at the end, one the user had running stays running
- Downloads (UTM, the guest image) happen only after the user has agreed to them, with the file, the source and the size named
- What the virtual machine cannot show (smooth video, delay, sound in sync) is a manual check by the user on their own Linux machine with Remmina (`docs/MANUAL-CHECKS.md`)
- Every error Tag has a route test
