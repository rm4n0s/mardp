# Glutter issues

What mardp works around in Glutter (CLAUDE.md, "Glutter"). Each entry says what is missing or broken, where the workaround is, and what the fix in Glutter would be. An entry is removed when the fix is in the Glutter version mardp uses (now v1.17.8, D23).

No entry at the moment. Glutter 1.17.8 took the five of 1.17.7, and mardp 1.0.4 dropped their workarounds:

- `glutter-macos build` honors `CGO_ENABLED` of the environment, signs with `-sign <identity>` and builds for `-min-macos` (it also writes it into the plist): `scripts/build-app.sh` lost its `go` shim, its second signing and its `plutil` edit.
- Glutter says why the app is asked to exit (`ui.PlatformDispatcher.ExitRequestReason`: the close button, a quit, the end of the session): `internal/ui` (`exitRequested`) answers by it, and `internal/mac/system` lost `WatchQuit`, which replaced the answer of Glutter's application delegate.
- The fake desktop of `gluttertest` serves the tray whatever the target platform, in a release build too: the window's tests no longer run as on Linux, nor skip without `debug_mode`.
