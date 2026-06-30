#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 0027

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
VERSION="${1:?usage: scripts/package-release.sh VERSION [OUTPUT_DIR]}"
OUTPUT_DIR="${2:-$ROOT/release-out}"
BINARY="${VECTOR_RELEASE_BINARY:-$ROOT/vector-linux-amd64}"
REQUIRED_GO="go1.26.4"

[[ "$VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$ ]] || {
  echo "invalid release version: $VERSION" >&2; exit 1;
}
[[ -f "$BINARY" && -x "$BINARY" && ! -L "$BINARY" ]] || {
  echo "missing executable release binary: $BINARY" >&2; exit 1;
}

build_info="$(go version -m "$BINARY")"
grep -Fq "$REQUIRED_GO" <<<"$build_info" || {
  echo "release binary was not built with $REQUIRED_GO" >&2
  printf '%s\n' "$build_info" >&2
  exit 1
}

rm -rf -- "$OUTPUT_DIR"
mkdir -p -- "$OUTPUT_DIR"
work="$(mktemp -d "${TMPDIR:-/tmp}/vector-release.XXXXXX")"
trap 'rm -rf -- "$work"' EXIT
stage="$work/vector-${VERSION#v}"
mkdir -p "$stage"

for path in \
 install.sh vector-total-purge.sh README.md LICENSE SECURITY.md PRIVACY.md \
 DEPLOYMENT.md THREAT_MODEL.md THIRD_PARTY_NOTICES.md SBOM.cdx.json \
 packaging; do
  [[ -e "$ROOT/$path" ]] || { echo "missing release input: $path" >&2; exit 1; }
  cp -a -- "$ROOT/$path" "$stage/"
done
install -o "$(id -u)" -g "$(id -g)" -m 0755 "$BINARY" "$stage/vector-linux-amd64"
printf '%s\n' "$build_info" > "$stage/BUILDINFO.txt"

if find "$stage" -type l -print -quit | grep -q .; then
  echo "release staging tree contains symbolic links" >&2; exit 1
fi
if find "$stage" -mindepth 1 ! -type f ! -type d -print -quit | grep -q .; then
  echo "release staging tree contains a non-regular entry" >&2; exit 1
fi

(
  cd "$stage"
  find . -type f ! -name MANIFEST.sha256 -print0 \
    | sort -z \
    | xargs -0 sha256sum \
    | sed 's#  \./#  #' > MANIFEST.sha256
  bash install.sh --verify-only
)

epoch="${SOURCE_DATE_EPOCH:-$(date +%s)}"
tar --sort=name --mtime="@$epoch" --owner=0 --group=0 --numeric-owner \
  -czf "$OUTPUT_DIR/vector-release.tar.gz" -C "$work" "$(basename "$stage")"
cp -- "$BINARY" "$OUTPUT_DIR/vector-linux-amd64"
printf '%s\n' "$build_info" > "$OUTPUT_DIR/BUILDINFO.txt"
(
  cd "$OUTPUT_DIR"
  sha256sum vector-release.tar.gz vector-linux-amd64 BUILDINFO.txt > SHA256SUMS
  sha256sum -c SHA256SUMS
)
printf 'release artifacts written to %s\n' "$OUTPUT_DIR"
