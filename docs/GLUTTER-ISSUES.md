# Glutter issues

What mardp works around in Glutter (CLAUDE.md, "Glutter"). Each entry says what is missing or broken, where the workaround is, and what the fix in Glutter would be. An entry is removed when the fix is in the Glutter version mardp uses (now v1.17.7, D23).

## glutter-macos builds without cgo

- **What:** `glutter-macos build` runs `go build` with `CGO_ENABLED=0` after the environment (`internal/mactool/build.go`, `compile`), and its command line has no flag for `BuildOptions.Env`. mardp's `internal/mac` packages need cgo (D14), so `./cmd/mardp` does not build that way.
- **Workaround:** `scripts/build-app.sh` puts a `go` of its own first in `PATH` for glutter-macos, which runs the real `go` with `CGO_ENABLED=1` (`# Glutter:` comment there).
- **Fix in Glutter:** honor `CGO_ENABLED` from the environment, or give `build` an `-env NAME=value` flag (as `run` and `shot` have) that fills `BuildOptions.Env`.

## glutter-macos signs only ad hoc

- **What:** `glutter-macos build` signs the bundle ad hoc; `SignOptions.Identity` has no flag. macOS ties Screen Recording and Accessibility to the signature, so an ad hoc build loses them at every build (SPEC §7.1, D22).
- **Workaround:** `scripts/build-app.sh` signs the bundle again with `scripts/sign.sh` (`# Glutter:` comment there).
- **Fix in Glutter:** a `-sign <identity>` flag for `build` that fills `SignOptions.Identity`.
