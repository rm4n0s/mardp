# The test machine

A Linux virtual machine on the Mac that runs FreeRDP and Remmina against mardp (SPEC §15.3, D16). It holds nothing of the owner's: no keys, no accounts, no shared folders. It is reached only through UTM's guest agent, never over SSH.

## What it is

| | |
|---|---|
| Host | UTM 5.0.9 (`v5.0.9-2`, `UTM.dmg`, 304,699,821 bytes, from github.com/utmapp/UTM/releases; Developer ID "Turing Software, LLC" (WDNLXAD4W8), notarized), in `/Applications` |
| Machine | `mardp-test`: QEMU backend, aarch64 `virt`, hardware virtualization, UEFI, 4 cores, 4 GiB, 25 GiB VirtIO disk, shared network (vmnet NAT), `virtio-gpu-gl-pci` display, `intel-hda` sound |
| Guest | Fedora 45 Cloud Base Generic, aarch64 (`Fedora-Cloud-Base-Generic-45-1.4.aarch64.qcow2`, 596,705,280 bytes, sha256 `1632acc8bc14d45b6c9596a0acc71b070d3748d1abdcf1a519be5c983bb0286c`, from dl.fedoraproject.org `/pub/fedora/linux/releases/45/Cloud/aarch64/images/`) |
| Set up by | cloud-init on the first boot, from `scripts/vm/user-data` (a seed disk labelled `cidata`) |
| User | `mardp`, no password (locked), auto-login into Xfce; the guest agent's commands run as root |
| SELinux | permissive: enforcing confines the guest agent's commands (`virt_qemu_ga_t` may not run `rpm`, `journalctl` or read most logs) |

Versions inside the guest are in "Versions" below.

## Making it

1. Download the two files above into `build/downloads/` (git ignores `build/`) and check the image's sha256 against Fedora's `CHECKSUM` file.
2. Install UTM: mount the image, check `codesign --verify --deep --strict` and `spctl -a -vv` (notarized Developer ID), copy `UTM.app` into `/Applications`. A file downloaded with `curl` has no quarantine flag, so macOS shows no "downloaded from the internet" question.
3. `scripts/vm/create.sh`: copies the image, builds the seed disk with `hdiutil makehybrid`, and creates the machine through UTM's AppleScript dictionary (`make new virtual machine`), then grows the system drive to 25 GiB (`guest size` of the drive and `update configuration`; UTM resizes the image). UTM copies both disks into its own machine bundle.
4. `scripts/vm.sh start`. On the first boot cloud-init sets SELinux to permissive, grows the partition, installs the packages of `user-data`, enables the guest agent and the graphical login, writes `/var/lib/mardp-setup-done` and reboots once. This takes several minutes.

Neither `utmctl` nor `osascript` asked for the Automation permission on this Mac (2026-11-19): §15.3 expected a question the first time; there was none.

## Using it

`scripts/vm.sh` wraps `utmctl`: `start`, `stop [--force]`, `status`, `exec <cmd...>`, `sh '<script>'`, `push`, `pull`, `address`, `host-address`. `start` remembers (in `build/vm/started-by-vm-sh`) that it started the machine, and `stop` stops only a machine `vm.sh` started, so a run leaves the machine as it found it.

Every command passes `--hide`, so UTM's main window does not come to the front; a running machine still has its display window.

## Notes

- The image's own size is 5 GiB. A `guest size` in the `make new virtual machine` call is ignored; changing it afterwards with `update configuration` works.
- UTM's `qemu-img` is a framework (a library), not a program; the resize goes through UTM.
- In AppleScript, `POSIX file` must be made outside the `tell application "UTM"` block, and a record inside a list is changed by building a new record (`{guest size:25600} & (item 1 of drv)`) and putting it back.
