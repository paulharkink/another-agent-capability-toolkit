#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
  echo "usage: verify-cli-checksum.sh CHECKSUMS_FILE ASSET_NAME BINARY_PATH" >&2
  exit 2
fi

checksums_file=$1
asset_name=$2
binary_path=$3
case "$asset_name" in
  argocd-linux-amd64|argocd-linux-arm64) ;;
  *) echo "Unsupported Argo CD CLI asset: $asset_name" >&2; exit 2 ;;
esac

if ! expected_hash=$(awk -v selected="$asset_name" '
  {
    sub(/\r$/, "")
    if ($2 == selected || $2 == "*" selected) {
      count++
      if (NF != 2 || length($1) != 64 || $1 ~ /[^[:xdigit:]]/) {
        invalid = 1
      } else {
        hash = tolower($1)
      }
    }
  }
  END {
    if (count != 1 || invalid) exit 1
    print hash
  }
' "$checksums_file"); then
  echo "Expected exactly one valid SHA-256 entry for $asset_name" >&2
  exit 1
fi

actual_hash=$(sha256sum "$binary_path" | awk '{print tolower($1)}')
if [ "$actual_hash" != "$expected_hash" ]; then
  echo "SHA-256 mismatch for $asset_name" >&2
  exit 1
fi
