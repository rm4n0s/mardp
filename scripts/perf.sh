#!/bin/sh
# Measures mardp by itself (SPEC §14, §15.5): mardp -headless serves its test
# pattern and a tone to rdptest on this Mac, and the numbers a machine can
# measure are printed. MARDP_PERF_SECONDS sets how long (default 60). It
# needs no permission and posts nothing to the Mac.
set -eu
cd "$(dirname "$0")/.."
TMPDIR=$(cd "${TMPDIR:-/tmp}" && pwd -P)/
export TMPDIR
seconds=${MARDP_PERF_SECONDS:-60}
exec go test -tags mardp_perf -run '^TestPerf$' -count=1 -v -timeout "$((seconds + 300))s" ./cmd/mardp/
