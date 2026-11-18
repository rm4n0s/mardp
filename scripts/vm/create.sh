#!/bin/sh
# Makes the mardp test machine in UTM from Fedora's cloud image (docs/VM.md).
# Needs build/downloads/<image> (see docs/VM.md for the file and checksum).
# The machine sets itself up on its first boot (scripts/vm/user-data) and
# reboots once; scripts/vm.sh then reaches it through the guest agent.
set -eu
cd "$(dirname "$0")/../.."
IMAGE=build/downloads/Fedora-Cloud-Base-Generic-45-1.4.aarch64.qcow2
VM=mardp-test
D="$PWD/build/vm"

if /Applications/UTM.app/Contents/MacOS/utmctl status "$VM" >/dev/null 2>&1; then
	echo "create.sh: a machine named $VM exists already" >&2
	exit 1
fi
rm -rf "$D" && mkdir -p "$D/seed"
cp scripts/vm/user-data scripts/vm/meta-data "$D/seed/"
hdiutil makehybrid -quiet -iso -joliet -default-volume-name cidata -o "$D/seed.iso" "$D/seed"
cp "$IMAGE" "$D/disk.qcow2"

osascript - "$D" <<'OSA'
on run argv
	set d to item 1 of argv
	set disk to POSIX file (d & "/disk.qcow2")
	set seed to POSIX file (d & "/seed.iso")
	tell application "UTM"
		set vm to make new virtual machine with properties {backend:qemu, configuration:{name:"mardp-test", architecture:"aarch64", memory:4096, cpu cores:4, hypervisor:true, uefi:true, drives:{{interface:VirtIO, source:disk}, {interface:VirtIO, raw:true, source:seed}}, network interfaces:{{hardware:"virtio-net-pci", mode:shared}}, displays:{{hardware:"virtio-gpu-gl-pci", dynamic resolution:true}}, sound devices:{{hardware:"intel-hda"}}}}
		-- grow the system drive to 25 GiB (guest size is in MiB); cloud-init grows the partition
		set cfg to configuration of vm
		set drv to drives of cfg
		set d1 to {guest size:25600} & (item 1 of drv)
		set item 1 of drv to d1
		set drives of cfg to drv
		update configuration of vm with cfg
		return id of vm
	end tell
end run
OSA
rm -f "$D/disk.qcow2" # UTM keeps its own copy inside the machine
