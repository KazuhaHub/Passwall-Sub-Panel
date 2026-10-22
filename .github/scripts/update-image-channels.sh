#!/usr/bin/env bash
set -euo pipefail
: "${IMAGE:?}" "${VERSION:?}" "${DIGEST:?}" "${STAGE_TOOL:?}"
[[ "$DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]
aliases=$("$STAGE_TOOL" aliases "$VERSION" "${APPROVED:-false}")
for alias in $aliases; do
 error=$(mktemp)
 if current=$(docker buildx imagetools inspect "$IMAGE:$alias" --format '{{json .Image}}' 2>"$error"); then
  current_version=$(printf '%s' "$current" | jq -er '[.. | objects | select(has("Labels")) | .Labels["org.opencontainers.image.version"] // empty] | unique | if length == 1 then .[0] else error("image has no unambiguous version") end')
  if [ "$current_version" != "$VERSION" ] && [ "$("$STAGE_TOOL" newer "$VERSION" "$current_version")" != true ]; then
   case "$alias" in stable|latest) echo "refusing stable rollback from $current_version to $VERSION" >&2; exit 1 ;; esac
   echo "keeping $alias at newer version $current_version"
   rm -f "$error"
   continue
  fi
 else
  if ! grep -Eiq 'manifest unknown|not found|no such manifest' "$error"; then cat "$error" >&2; rm -f "$error"; exit 1; fi
 fi
 rm -f "$error"
 if [ "${CHECK_ONLY:-false}" = true ]; then continue; fi
 docker buildx imagetools create -t "$IMAGE:$alias" "$IMAGE@$DIGEST"
 actual=$(docker buildx imagetools inspect "$IMAGE:$alias" --format '{{json .Manifest}}' | jq -er .digest)
 test "$actual" = "$DIGEST" || { echo "$alias has not converged to the verified digest" >&2; exit 1; }
done
