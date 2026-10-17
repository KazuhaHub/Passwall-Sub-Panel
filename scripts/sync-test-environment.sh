#!/usr/bin/env bash
# Update only the existing local PSP fixture; never seed or recreate its data.
set -euo pipefail
umask 077

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_root=/private/tmp/psp-manual-tqGJIe
test_compose="$test_root/compose.yaml"
panel_container=psp-manual-panel-tqGJIe
xui_container=psp-manual-xui-tqGJIe
project=psp-manual-tqgjie
base_url=http://127.0.0.1:18788

[[ -f "$test_compose" && -d "$test_root/main-runtime" ]] || { echo 'Existing manual fixture is missing; refusing to create or seed a replacement.' >&2; exit 1; }
[[ $(docker inspect "$panel_container" --format '{{index .Config.Labels "com.docker.compose.project"}}') == "$project" ]] || { echo 'Unexpected Compose project.' >&2; exit 1; }
[[ $(docker inspect "$panel_container" --format '{{range .Mounts}}{{if eq .Destination "/manual"}}{{.Source}}{{end}}{{end}}') == "$test_root" ]] || { echo 'Unexpected fixture data mount.' >&2; exit 1; }
[[ $(docker inspect "$panel_container" --format '{{json .Config.Cmd}}') == '["--config","/manual/main-runtime/config/config.yaml"]' ]] || { echo 'Unexpected panel config target.' >&2; exit 1; }
[[ -x "$repo_root/web-react/node_modules/.bin/vite" ]] || { echo 'Frontend dependencies are missing; install the lockfile dependencies first.' >&2; exit 1; }

go_bin=${PSP_TEST_GO:-}
if [[ -z "$go_bin" ]]; then
  if command -v go >/dev/null 2>&1; then go_bin=$(command -v go)
  elif [[ -x /private/tmp/psp-network-tools.GMPtn3/go/bin/go ]]; then go_bin=/private/tmp/psp-network-tools.GMPtn3/go/bin/go
  else echo 'Go toolchain missing; set PSP_TEST_GO to the project toolchain.' >&2; exit 1
  fi
fi
case $(docker exec "$panel_container" uname -m) in
  aarch64) target_arch=arm64 ;;
  x86_64) target_arch=amd64 ;;
  *) echo 'Unsupported fixture architecture.' >&2; exit 1 ;;
esac

stamp=$(TZ=Asia/Shanghai date +%Y%m%d-%H%M%S)
commit=$(git -C "$repo_root" rev-parse --short=8 HEAD)
build_version="test-$commit-$stamp"
artifact="psp-test-$stamp"
current_link="$test_root/psp-test-current"
old_entry=$(docker inspect "$panel_container" --format '{{index .Config.Entrypoint 0}}')
previous=${old_entry#/manual/}
if [[ -L "$current_link" ]]; then previous=$(readlink "$current_link")
elif [[ -e "$current_link" ]]; then echo 'Refusing to overwrite a non-symlink current artifact.' >&2; exit 1
fi
[[ "$previous" == psp* && "$previous" != */* && -f "$test_root/$previous" ]] || { echo 'Cannot determine a safe rollback artifact.' >&2; exit 1; }
if [[ ! -L "$current_link" ]]; then ln -s "$previous" "$current_link"; fi
xui_started=$(docker inspect "$xui_container" --format '{{.State.StartedAt}}')

cd "$repo_root/web-react"
npm run build
cd "$repo_root"
build_date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
env CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" GOTOOLCHAIN=local GOWORK=off \
  GOCACHE="${PSP_TEST_GOCACHE:-/private/tmp/psp-manual-go-cache}" \
  "$go_bin" build -trimpath \
  -ldflags="-s -w -X github.com/KazuhaHub/passwall-sub-panel/internal/version.Version=$build_version -X github.com/KazuhaHub/passwall-sub-panel/internal/version.Commit=$commit-worktree -X github.com/KazuhaHub/passwall-sub-panel/internal/version.BuildDate=$build_date" \
  -o "$test_root/$artifact" ./cmd/panel

rollback() {
  echo "Sync failed; restarting the previous PSP artifact: $previous" >&2
  ln -sfn "$previous" "$current_link"
  docker compose -p "$project" -f "$test_compose" up -d --no-deps --force-recreate panel
}
trap rollback ERR
docker compose -p "$project" -f "$test_compose" stop panel
backup="$test_root/runtime-backup-$stamp.tar.gz"
tar -czf "$backup" -C "$test_root" main-runtime
ln -sfn "$artifact" "$current_link"
docker compose -p "$project" -f "$test_compose" up -d --no-deps --force-recreate panel

ready=false
for attempt in {1..40}; do
  if curl --fail --silent --max-time 2 "$base_url/api/version" | node -e '
    let raw=""; process.stdin.on("data", b => raw += b); process.stdin.on("end", () => {
      try { process.exit(JSON.parse(raw).version === process.argv[1] ? 0 : 1) } catch { process.exit(1) }
    });' "$build_version" && curl --fail --silent --max-time 2 "$base_url/health" >/dev/null; then
    ready=true
    break
  fi
  sleep 1
done
[[ "$ready" == true ]]
[[ $(docker inspect "$xui_container" --format '{{.State.StartedAt}}') == "$xui_started" ]]
trap - ERR
printf 'Test environment synced: %s\nVersion: %s\nRollback artifact: %s\nData backup: %s\n' "$base_url" "$build_version" "$previous" "$backup"
