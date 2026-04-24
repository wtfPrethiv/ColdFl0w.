#!/bin/sh
set -eu
version=${1:-0.1.0}
mkdir -p dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
    platform=${target%/*}
    arch=${target#*/}
    suffix=''
    if [ "$platform" = windows ]; then suffix=.exe; fi
    echo "Building $target"
    CGO_ENABLED=0 GOOS="$platform" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "dist/coldflow-$platform-$arch$suffix" ./cmd/coldflow
done
if command -v sha256sum >/dev/null 2>&1; then
    (cd dist && sha256sum coldflow-* > SHA256SUMS)
else
    (cd dist && shasum -a 256 coldflow-* > SHA256SUMS)
fi
