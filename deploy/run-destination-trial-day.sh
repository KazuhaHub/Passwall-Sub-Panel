#!/usr/bin/env bash
# Run manually on the separately authorized disposable Linux host.
set -euo pipefail
umask 077
if test "$#" != 2; then
  printf '%s\n' 'usage: run-destination-trial-day.sh CLEAN_NODE_CHECKOUT NEW_ABSOLUTE_OUTPUT_DIRECTORY' >&2
  exit 1
fi
test "$(id -u)" = 0
test "$(uname -s)" = Linux
repo=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
node_repo=$(cd -- "$1" && pwd -P)
run_dir=$2
case "$run_dir" in /*) ;; *) exit 1 ;; esac
test ! -e "$run_dir"
test -z "$(git -C "$repo" status --porcelain)"
test -z "$(git -C "$node_repo" status --porcelain)"
node_sha=61f30239e09d62b05bd59df7d9d28a5071790556
test "$(git -C "$node_repo" rev-parse HEAD)" = "$node_sha"
panel_sha=$(git -C "$repo" rev-parse HEAD)
test "${#panel_sha}" = 40
for source in "$repo" "$node_repo"; do
  test "$(awk '$1 == "github.com/KazuhaHub/passwall-protocol" {print $2}' "$source/go.mod")" = v0.2.1-0.20261009195400-11a90e9bc95a
  if grep -Eq '^[[:space:]]*replace([[:space:]]|\()' "$source/go.mod"; then exit 1; fi
done
mkdir -m 700 -- "$run_dir"
run_dir=$(cd -- "$run_dir" && pwd -P)
export RUNNER_TEMP="$run_dir" TMPDIR="$run_dir" GOWORK=off CGO_ENABLED=0
export GOOS=linux
GOARCH=$(go env GOHOSTARCH)
export GOARCH
export PSP_NATIVE_TRIAL_DAY=24h PSP_NATIVE_DB_KIND=sqlite PSP_NATIVE_DB_DSN=''
export PSP_NATIVE_CORE_VERSION=26.6.27 PSP_NATIVE_NODE_BIN="$run_dir/destination-node"
export PSP_NATIVE_LOAD_METRICS="$run_dir/destination-trial-day-bootstrap-metrics.json"
export PSP_NATIVE_TRIAL_DAY_METRICS="$run_dir/destination-trial-day-metrics.json"
node_toolchain=$(awk '$1 == "toolchain" {print $2}' "$node_repo/go.mod")
panel_toolchain=$(awk '$1 == "toolchain" {print $2}' "$repo/go.mod")
test -n "$node_toolchain"
test -n "$panel_toolchain"
cd -- "$node_repo"
GOTOOLCHAIN="$node_toolchain" go build -trimpath -buildvcs=true -o "$PSP_NATIVE_NODE_BIN" ./cmd/node >"$run_dir/node-build.private.log" 2>&1
chmod 700 "$PSP_NATIVE_NODE_BIN"
go version -m "$PSP_NATIVE_NODE_BIN" >"$run_dir/node-buildinfo.private.txt"
grep -F "vcs.revision=$node_sha" "$run_dir/node-buildinfo.private.txt" >/dev/null
grep -F 'vcs.modified=false' "$run_dir/node-buildinfo.private.txt" >/dev/null
cd -- "$repo"
GOTOOLCHAIN="$panel_toolchain" go test -c -buildvcs=true -tags destination_acceptance -o "$run_dir/trial-day.test" ./internal/app >"$run_dir/panel-build.private.log" 2>&1
test "$(git rev-parse HEAD)" = "$panel_sha"
test -z "$(git status --porcelain)"
test "$(git -C "$node_repo" rev-parse HEAD)" = "$node_sha"
test -z "$(git -C "$node_repo" status --porcelain)"
printf '%s\n' "$panel_sha" >"$run_dir/panel-source.txt"
printf '%s\n' "$node_sha" >"$run_dir/node-source.txt"
sha256sum "$run_dir/trial-day.test" "$PSP_NATIVE_NODE_BIN" >"$run_dir/binaries.private.sha256"
set +e
"$run_dir/trial-day.test" -test.v -test.count=1 -test.run '^TestNativeDestinationTrialRealDay$' -test.timeout=26h 2>&1 |
  GOTOOLCHAIN="$panel_toolchain" go tool test2json -p github.com/KazuhaHub/passwall-sub-panel/internal/app -t >"$run_dir/trial-day-raw.private.jsonl"
statuses=("${PIPESTATUS[@]}")
set -e
node deploy/destination-trial-day.mjs "$run_dir/trial-day-raw.private.jsonl" "$PSP_NATIVE_TRIAL_DAY_METRICS" "$PSP_NATIVE_LOAD_METRICS" "$run_dir/destination-trial-day-tests.json"
test "${statuses[0]}" = 0
test "${statuses[1]}" = 0
