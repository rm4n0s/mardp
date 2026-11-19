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

## Cmd+Q, the close button and a logout are one request on macOS

- **What:** on macOS, the window's close button, Cmd+Q (`applicationShouldTerminate:`, `requestQuit`) and the question macOS asks every app at a logout, restart or shutdown all reach the app as `OnExitRequested` (through `OnCloseRequest`), with nothing that tells them apart. mardp hides its window on a close (SPEC §7.3), so it hides it on Cmd+Q and at a logout too. Glutter also answers `applicationShouldTerminate:` with `NSTerminateCancel` before the app has decided, which cancels a logout even for an app that then quits.
- **Workaround:** Cmd+Q hides the window and Quit is in the menu-bar icon's menu only (`internal/ui/window.go`, `exitRequested`, `// Glutter:` comment there). For a logout, `cmd/mardp/mac.go` (`window`) watches `NSWorkspaceWillPowerOffNotification` (`internal/mac/system`, `WatchPowerOff`) and ends the window's context, which quits the app (`// Glutter:` comment there).
- **Fix in Glutter:** say why the app is asked to exit (the close button, the app's Quit, the system's logout or shutdown), for example a field of `AppExitType` or an argument of `OnExitRequested`; answer `applicationShouldTerminate:` with `NSTerminateLater` and `replyToApplicationShouldTerminate:` once the app has decided.

## The fake desktop serves only the Linux tray

- **What:** `gluttertest.FakeDesktop` serves the tray over a fake D-Bus (StatusNotifierItem), so a test of a `desktop.SystemTray` must run as on Linux; without it the tray reports `TrayUnavailable` and the test fails. The macOS tray (`NSStatusItem`) has no fake.
- **Workaround:** the window's tests set the target platform to Linux (`internal/ui/ui_test.go`, `newDesktopTester`, `// Glutter:` comment there); the icon in the macOS menu bar is a manual check (docs/MANUAL-CHECKS.md, 5.3).
- **Fix in Glutter:** a fake of the macOS tray in `gluttertest`, or a fake tray that does not depend on the platform.
