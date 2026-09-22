#!/usr/bin/env bash
set -euo pipefail
version=${1:?usage: scripts/release.sh vX.Y.Z}
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo 'Version must be vX.Y.Z' >&2
  exit 1
fi
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
mkdir -p dist
for arch in amd64 arm64; do
  name="cairn_${version}_linux_${arch}"
  stage=$(mktemp -d)
  mkdir -p "$stage/$name"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="-s -w -X main.version=$version" -o "$stage/$name/cairn" ./cmd/cairn
  cp LICENSE THIRD_PARTY_NOTICES.md README.md CHANGELOG.md "$stage/$name/"
  cp -R docs deploy "$stage/$name/"
  tar -czf "dist/$name.tar.gz" -C "$stage" "$name"
  rm -rf "$stage"
done
python3 - "$version" <<'PY'
import hashlib,pathlib,sys
version=sys.argv[1]
files=sorted(pathlib.Path('dist').glob(f'cairn_{version}_linux_*.tar.gz'))
pathlib.Path('dist/checksums.txt').write_text(''.join(hashlib.sha256(f.read_bytes()).hexdigest()+'  '+f.name+'\n' for f in files))
PY
