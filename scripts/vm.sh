#!/bin/sh
# Drives the mardp test machine in UTM (SPEC §15.3, D16, docs/VM.md) through
# UTM's utmctl and its guest agent. There is no SSH.
#
#   vm.sh start            start it; remembers whether this script started it
#   vm.sh stop [--force]   stop it, only if vm.sh started it (or with --force)
#   vm.sh status           stopped, started, ...
#   vm.sh exec <cmd...>    run a command in the guest as root; prints its
#                          output and returns its exit code
#   vm.sh sh '<script>'    run a shell script in the guest (as root)
#   vm.sh push <local> <guest path>
#   vm.sh pull <guest path> <local>
#   vm.sh address          the guest's IPv4 address
#   vm.sh host-address     the Mac's address as the guest reaches it
#
# A run leaves the machine as it found it: a machine the run started is
# stopped at the end, one that was already running stays running.
set -eu

VM="${MARDP_VM:-mardp-test}"
UTMCTL=/Applications/UTM.app/Contents/MacOS/utmctl
STATE="$(cd "$(dirname "$0")/.." && pwd)/build/vm/started-by-vm-sh"

if [ ! -x "$UTMCTL" ]; then
	echo "vm.sh: UTM is not installed in /Applications (docs/VM.md)" >&2
	exit 1
fi


status() { "$UTMCTL" status "$VM" 2>/dev/null | tail -n 1; }

wait_agent() {
	i=0
	while [ $i -lt 90 ]; do
		if "$UTMCTL" exec --hide "$VM" --cmd /bin/true >/dev/null 2>&1; then
			return 0
		fi
		i=$((i + 1))
		sleep 2
	done
	echo "vm.sh: the guest agent did not answer within 3 minutes" >&2
	return 1
}

cmd=${1:-}
[ $# -gt 0 ] && shift
case "$cmd" in
start)
	s=$(status)
	if [ "$s" = started ]; then
		echo "already running"
	else
		"$UTMCTL" start --hide "$VM" 2>/dev/null
		mkdir -p "$(dirname "$STATE")"
		: >"$STATE"
	fi
	wait_agent
	;;
stop)
	if [ "$(status)" = stopped ]; then
		rm -f "$STATE"
	elif [ -f "$STATE" ] || [ "${1:-}" = --force ]; then
		"$UTMCTL" stop "$VM" 2>/dev/null
		rm -f "$STATE"
	else
		echo "left running: vm.sh did not start it (use --force)"
	fi
	;;
status)
	status
	;;
exec)
	[ $# -gt 0 ] || { echo "usage: vm.sh exec <cmd...>" >&2; exit 2; }
	exec "$UTMCTL" exec --hide "$VM" --cmd "$@" 2>/dev/null
	;;
sh)
	[ $# -eq 1 ] || { echo "usage: vm.sh sh '<script>'" >&2; exit 2; }
	exec "$UTMCTL" exec --hide "$VM" --cmd /bin/sh -c "$1" 2>/dev/null
	;;
push)
	[ $# -eq 2 ] || { echo "usage: vm.sh push <local> <guest path>" >&2; exit 2; }
	"$UTMCTL" file push --hide "$VM" "$2" <"$1" 2>/dev/null
	;;
pull)
	[ $# -eq 2 ] || { echo "usage: vm.sh pull <guest path> <local>" >&2; exit 2; }
	"$UTMCTL" file pull --hide "$VM" "$1" >"$2" 2>/dev/null
	;;
address)
	"$UTMCTL" ip-address --hide "$VM" 2>/dev/null | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1
	;;
host-address)
	# UTM's shared network is a vmnet NAT; the Mac is the guest's gateway.
	"$UTMCTL" exec --hide "$VM" --cmd /bin/sh -c "ip -4 route show default | awk '{print \$3; exit}'" 2>/dev/null
	;;
*)
	sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
	;;
esac
