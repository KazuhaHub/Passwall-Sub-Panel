#!/usr/bin/env bash
#
# Partitions the module's packages into balanced shards for the race job.
#
# THE PACKAGE LIST COMES FROM `go list`, NEVER FROM THE WEIGHTS FILE. A package
# added tomorrow is therefore in a shard by construction, and the weights file
# only decides WHICH shard. That is the property worth protecting: a sharding
# scheme that silently dropped a package would turn the suite's canonical race
# run into a check that passes without testing anything — a failure the
# single-job version could not have.
#
# Emits a GitHub Actions matrix as JSON on stdout.
#
# Deliberately bash 3.2-compatible (no mapfile, no associative arrays) so the
# planner can be exercised on a developer machine before it is trusted with the
# merge gate.
set -euo pipefail

shards=4
weights=""
default_weight=3

while [ $# -gt 0 ]; do
  case "$1" in
    --shards) shards="$2"; shift 2 ;;
    --weights) weights="$2"; shift 2 ;;
    --default) default_weight="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

case "$shards" in
  ''|*[!0-9]*) echo "shard count must be a positive integer, got '$shards'" >&2; exit 2 ;;
esac
if [ "$shards" -lt 1 ]; then
  echo "shard count must be at least 1, got '$shards'" >&2
  exit 2
fi
if [ -n "$weights" ] && [ ! -f "$weights" ]; then
  echo "weights file not found: $weights" >&2
  exit 1
fi

# Every package, not only the ones with tests: `go test ./...` also type-checks
# the rest, and a sharded run that quietly stopped compiling them would be a
# regression in what the job proves.
count=$(go list ./... | awk 'END { print NR }')
if [ "$count" -eq 0 ]; then
  # An empty matrix would let the aggregate job pass having run nothing.
  echo "go list returned no packages; refusing to plan an empty run" >&2
  exit 1
fi

go list ./... | awk -v n="$shards" -v weights="$weights" -v fallback="$default_weight" '
  BEGIN {
    if (weights != "") {
      while ((getline line < weights) > 0) {
        sub(/[[:space:]]*#.*/, "", line)
        if (line ~ /^[[:space:]]*$/) continue
        if (split(line, field, /[[:space:]]+/) < 2) continue
        measured[field[2]] = field[1] + 0
      }
      close(weights)
    }
    for (i = 1; i <= n; i++) { load[i] = 0; shard[i] = "" }
  }
  NF {
    total++
    weight[total] = ($0 in measured) ? measured[$0] : fallback + 0
    name[total] = $0
  }
  END {
    # Longest-processing-time first. Selection sort: n is a few dozen, so the
    # quadratic cost is irrelevant next to spawning a runner.
    for (i = 1; i <= total; i++) {
      pick = i
      for (j = i + 1; j <= total; j++) if (weight[j] > weight[pick]) pick = j
      if (pick != i) {
        swap = weight[i]; weight[i] = weight[pick]; weight[pick] = swap
        swap = name[i]; name[i] = name[pick]; name[pick] = swap
      }
    }
    for (i = 1; i <= total; i++) {
      best = 1
      for (j = 2; j <= n; j++) if (load[j] < load[best]) best = j
      load[best] += weight[i]
      shard[best] = (shard[best] == "" ? "" : shard[best] " ") name[i]
    }
    # `include` rather than a named dimension: fromJSON takes the matrix OBJECT,
    # so emitting {"shard":[{...}]} would define one dimension called "shard"
    # whose values are objects, leaving `matrix.packages` empty in the steps
    # while the job names still rendered correctly.
    printf "{\"include\":["
    for (i = 1; i <= n; i++) {
      printf "%s{\"index\":\"%d\",\"packages\":\"%s\"}", (i > 1 ? "," : ""), i - 1, shard[i]
    }
    printf "]}"
  }'
echo
