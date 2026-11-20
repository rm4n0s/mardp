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
- **Workaround:** mardp takes the quit before Glutter sees it: `internal/mac/system` (`WatchQuit`) replaces the answer of Glutter's application delegate to `applicationShouldTerminate:`, which the app menu's Quit, Cmd+Q, the Dock's Quit, a quit Apple Event and a logout all ask (1.0.2 replaced `-[NSApplication terminate:]`, which the Dock's Quit does not send), and `cmd/mardp/mac.go` (`window`) ends the window's context there, which quits the app (`// Glutter:` comments in both). Until 1.0.2 Cmd+Q and the app menu's Quit hid the window, and the owner took Quit for broken. So `OnExitRequested` now comes from the close button alone, and the window hides (`internal/ui/window.go`, `exitRequested`). For a logout, `cmd/mardp/mac.go` also watches `NSWorkspaceWillPowerOffNotification` (`internal/mac/system`, `WatchPowerOff`).
- **Fix in Glutter:** say why the app is asked to exit (the close button, the app's Quit, the system's logout or shutdown), for example a field of `AppExitType` or an argument of `OnExitRequested`; answer `applicationShouldTerminate:` with `NSTerminateLater` and `replyToApplicationShouldTerminate:` once the app has decided.

## The fake desktop serves only the Linux tray

- **What:** `gluttertest.FakeDesktop` serves the tray over a fake D-Bus (StatusNotifierItem), so a test of a `desktop.SystemTray` must run as on Linux; without it the tray reports `TrayUnavailable` and the test fails. The macOS tray (`NSStatusItem`) has no fake.
- **Workaround:** the window's tests set the target platform to Linux (`internal/ui/ui_test.go`, `newDesktopTester`, `// Glutter:` comment there); the icon in the macOS menu bar is a manual check (docs/MANUAL-CHECKS.md, 5.3).
- **Fix in Glutter:** a fake of the macOS tray in `gluttertest`, or a fake tray that does not depend on the platform.

## glutter-macos builds for the macOS that builds
- **What:** `glutter-macos build` writes `LSMinimumSystemVersion` 12.0 into `Info.plist` but leaves the deployment target of the cgo code and the link to clang, whose default is the macOS of the building machine (27.2 on the owner's Mac), so the executable does not open on older systems. It has no flag for the minimum either; mardp's is macOS 14 (SPEC D20).
- **Workaround:** `scripts/build-app.sh` sets `MACOSX_DEPLOYMENT_TARGET=14.0` in its `go` shim, writes `LSMinimumSystemVersion` 14.0 with `plutil` before signing, and checks the executable's `minos` (`# Glutter:` comments there).
- **Fix in Glutter:** build with `MACOSX_DEPLOYMENT_TARGET` set to the minimum it writes, and take `-min-macos` for both.
