#!/usr/bin/env sh
set -eu

architecture="${TARGETARCH:-}"
if [ -z "$architecture" ]; then
  architecture="$(dpkg --print-architecture)"
fi
case "$architecture" in
  amd64|arm64) printf '%s\n' "$architecture" ;;
  *) printf 'Unsupported architecture: %s\n' "$architecture" >&2; exit 1 ;;
esac
