#!/bin/sh
# Inspect every release platform without executing foreign binaries. The Go
# provenance must match the immutable release SHA independently of --version.
set -eu
[ "$#" = 4 ] || { printf '%s\n' 'usage: check-build.sh BINARY GOOS GOARCH COMMIT' >&2; exit 1; }
binary=$1
goos=$2
goarch=$3
commit=$4
toolchain=$(awk '$1 == "toolchain" { print $2 }' go.mod)
[ -n "$toolchain" ] || { printf '%s\n' 'missing pinned toolchain' >&2; exit 1; }
info=$(go version -m "$binary")
printf '%s\n' "$info" | awk -v toolchain="$toolchain" -v goos="$goos" -v goarch="$goarch" -v commit="$commit" '
    NR == 1 { compiler = ($NF == toolchain) }
    $1 == "build" && $2 == "GOOS=" goos { os = 1 }
    $1 == "build" && $2 == "GOARCH=" goarch { arch = 1 }
    $1 == "build" && $2 == "vcs.revision=" commit { revision = 1 }
    $1 == "build" && $2 == "vcs.modified=false" { clean = 1 }
    $1 == "build" && $2 == "CGO_ENABLED=0" { static = 1 }
    $1 == "path" && $2 == "github.com/KazuhaHub/passwall-sub-panel/cmd/panel" { main = 1 }
    END { exit !(compiler && os && arch && revision && clean && static && main) }
' || { printf '%s\n' 'release compiler, platform or clean commit provenance mismatch' >&2; exit 1; }
printf '%s\n' "Verified $goos/$goarch compiler $toolchain and clean source commit."
