#!/usr/bin/env bash
# Package only known source/docs; local configuration and credentials stay out.
set -euo pipefail

if [[ $# -ne 1 || ! $1 =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Usage: bash scripts/package.sh vMAJOR.MINOR.PATCH" >&2
  exit 2
fi
release_tag=$1
version=${release_tag#v}
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
output_dir="$repo_root/dist/$release_tag"
if [[ -e "$output_dir" ]]; then
  echo "Output directory already exists: $output_dir" >&2
  exit 1
fi
mkdir -p "$output_dir"

for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  target_os=${target%/*}
  target_arch=${target#*/}
  package_name="workbuddy-cli_${release_tag}_${target_os}_${target_arch}"
  stage="$output_dir/$package_name"
  mkdir -p "$stage/docs"
  executable=workbuddy-cli
  if [[ $target_os == windows ]]; then executable=workbuddy-cli.exe; fi
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build \
    -trimpath -ldflags "-s -w -X workbuddy-cli/internal/cli.Version=$version" \
    -o "$stage/$executable" ./cmd/workbuddy-cli
  cp README.md THIRD_PARTY_LICENSES config.example.toml "$stage/"
  cp docs/scheduling.md docs/upstream-sync.md "$stage/docs/"
  if [[ $target_os == windows ]]; then
    (cd "$output_dir" && zip -qr "$package_name.zip" "$package_name")
  else
    tar -czf "$output_dir/$package_name.tar.gz" -C "$output_dir" "$package_name"
  fi
  printf 'Packaged %s\n' "$package_name"
done

(cd "$output_dir" && shasum -a 256 ./*.tar.gz ./*.zip > SHA256SUMS)
(cd "$output_dir" && shasum -a 256 -c SHA256SUMS)
