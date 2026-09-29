#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
go_bin="${GO:-go}"
if ! command -v "$go_bin" >/dev/null && [[ -x .tools/go/bin/go ]]; then go_bin="$PWD/.tools/go/bin/go"; fi
mkdir -p dist/releases bin
version="$(cat VERSION)"
ldflags="-s -w -X github.com/xiaoliu-heng/homefleet/internal/model.Version=$version"
(cd web && npm ci && npm run build)
"$go_bin" build -trimpath -ldflags="$ldflags" -o bin/homefleet-hub ./cmd/hub
for target in linux/amd64 linux/arm64 darwin/arm64 windows/amd64; do
  os="${target%/*}"; arch="${target#*/}"; suffix=''
  [[ "$os" != windows ]] || suffix='.exe'
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 "$go_bin" build -trimpath -ldflags="$ldflags" -o "dist/releases/homefleet-agent-$os-$arch$suffix" ./cmd/agent
done
"$go_bin" run ./cmd/release -dir dist/releases -version "$version"
cp scripts/bootstrap-* scripts/install-* scripts/uninstall-* dist/releases/
cp LICENSE THIRD_PARTY_NOTICES.md dist/releases/
(cd dist/releases && if command -v sha256sum >/dev/null; then sha256sum homefleet-agent-* agent-release.json bootstrap-* install-* uninstall-* LICENSE THIRD_PARTY_NOTICES.md > SHA256SUMS; else shasum -a 256 homefleet-agent-* agent-release.json bootstrap-* install-* uninstall-* LICENSE THIRD_PARTY_NOTICES.md > SHA256SUMS; fi)
