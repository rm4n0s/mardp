#!/bin/sh
# Measures mardp by itself (SPEC §14, §15.5). MARDP_PERF_SECONDS sets how
# long.
#
#   scripts/perf.sh        mardp -headless serves its test pattern and a
#                          tone to rdptest on this Mac (default 60 s); it
#                          needs no permission and posts nothing to the Mac.
#   scripts/perf.sh idle   the same with the pattern still and nothing
#                          playing (default 300 s): the CPU of an idle
#                          session (SPEC §11 rule 6); no permission either.
#   scripts/perf.sh screen mardp, as a bundle signed as the M0 probe,
#                          captures the real screen and its sound for
#                          rdptest (default 300 s), and reports the same;
#                          the screen may not be still. It uses the probe's
#                          Screen Recording, is view-only and plays the
#                          sound on both, so it posts nothing and never
#                          touches the Mac's output.
set -eu
cd "$(dirname "$0")/.."
TMPDIR=$(cd "${TMPDIR:-/tmp}" && pwd -P)/
export TMPDIR
case "${1:-}" in
"") test='^TestPerf$' seconds=${MARDP_PERF_SECONDS:-60} ;;
idle) test='^TestPerfIdle$' seconds=${MARDP_PERF_SECONDS:-300} ;;
screen) test='^TestPerfScreen$' seconds=${MARDP_PERF_SECONDS:-300} ;;
*)
	echo "usage: scripts/perf.sh [idle|screen]" >&2
	exit 2
	;;
esac
exec go test -tags mardp_perf -run "$test" -count=1 -v -timeout "$((seconds + 300))s" ./cmd/mardp/
