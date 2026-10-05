#!/bin/sh
# Bootstrap only: installed Unix launchers point directly to the native binary.
set -eu
VERSION=${AACT_VERSION:-0.1.0-dev}
BASE=${AACT_DOWNLOAD_BASE:-https://github.com/paulharkink/another-agent-capability-toolkit/releases/download}
ROOT=${AACT_INSTALL_DIR:-${HOME:?HOME is required}/.local/share/aact}
PRINT_TARGET=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    -v|--version) [ "$#" -ge 2 ] || { echo '-v/--version needs a value' >&2; exit 1; }; VERSION=$2; shift 2 ;;
    --base-url) [ "$#" -ge 2 ] || { echo '--base-url needs a value' >&2; exit 1; }; BASE=$2; shift 2 ;;
    --install-dir) [ "$#" -ge 2 ] || { echo '--install-dir needs a value' >&2; exit 1; }; ROOT=$2; shift 2 ;;
    --print-target) PRINT_TARGET=1; shift ;;
    --help) echo 'Usage: install.sh [-v|--version VERSION] [--install-dir ROOT] [--base-url URL] [--print-target]'; exit 0 ;;
    *) printf 'Unknown installer argument: %s\n' "$1" >&2; exit 1 ;;
  esac
done
case "$VERSION" in ''|*[!A-Za-z0-9._-]*|.|..) echo 'Invalid release version' >&2; exit 1 ;; esac
system=${AACT_OS:-$(uname -s)}
case "$system" in Darwin|darwin|macos) platform=darwin ;; Linux|linux) platform=linux ;; MINGW*|MSYS*|CYGWIN*|Windows_NT|windows) platform=windows ;; *) printf 'Unsupported operating system: %s\n' "$system" >&2; exit 1 ;; esac
machine=${AACT_ARCH:-$(uname -m)}
case "$machine" in x86_64|amd64|AMD64|x64) arch=amd64 ;; aarch64|arm64|ARM64) arch=arm64 ;; *) printf 'Unsupported architecture: %s\n' "$machine" >&2; exit 1 ;; esac
if [ "$PRINT_TARGET" = 1 ]; then printf '%s_%s\n' "$platform" "$arch"; exit 0; fi
extension=tar.gz
[ "$platform" != windows ] || extension=zip
filename="aact_${VERSION}_${platform}_${arch}.${extension}"
download() {
  if command -v curl >/dev/null 2>&1; then curl --fail --location --silent --show-error "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -q "$1" -O "$2"
  else echo 'curl or wget is required to download the release' >&2; return 1; fi
}
mkdir -p "$ROOT/releases" "$ROOT/bin"
ROOT=$(cd "$ROOT" && pwd)
work=$(mktemp -d "$ROOT/releases/.install-XXXXXX")
launcher="$ROOT/bin/.aact-next-$$"
trap 'rm -rf "$work"; rm -f "$launcher"' EXIT HUP INT TERM
download "${BASE%/}/v${VERSION}/${filename}" "$work/$filename"
download "${BASE%/}/v${VERSION}/checksums.txt" "$work/checksum"
expected=$(awk -v name="$filename" '$2 == name { print $1; exit }' "$work/checksum")
case "$expected" in *[!a-fA-F0-9]*|'') echo 'Invalid release checksum' >&2; exit 1 ;; esac
[ "${#expected}" = 64 ] || { echo 'Invalid release checksum length' >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$work/$filename" | sed 's/[[:space:]].*//')
elif command -v shasum >/dev/null 2>&1; then actual=$(shasum -a 256 "$work/$filename" | sed 's/[[:space:]].*//')
else echo 'sha256sum or shasum is required for checksum verification' >&2; exit 1; fi
expected=$(printf '%s' "$expected" | tr 'A-F' 'a-f')
[ "$actual" = "$expected" ] || { echo 'Release checksum mismatch; existing installation preserved' >&2; exit 1; }
mkdir "$work/tree"
check_names() {
  while IFS= read -r entry; do
    case "$entry" in /*|\\*|../*|*/../*|*/..|*[![:print:]]*) echo 'Unsafe archive path' >&2; return 1 ;; esac
  done
}
if [ "$extension" = zip ]; then
  command -v unzip >/dev/null 2>&1 || { echo 'unzip is required for the MinGW bootstrap; use install.ps1 for native Windows' >&2; exit 1; }
  unzip -Z1 "$work/$filename" > "$work/names"
  check_names < "$work/names"
  unzip -q "$work/$filename" -d "$work/tree"
else
  tar -tzf "$work/$filename" > "$work/names"
  check_names < "$work/names"
  # Releases contain only directories and regular files; reject link entries.
  tar -tvzf "$work/$filename" > "$work/types"
  links=$(sed -n '/^[lh]/p' "$work/types")
  [ -z "$links" ] || { echo 'Archive links are not allowed' >&2; exit 1; }
  tar -xzf "$work/$filename" -C "$work/tree"
fi
binary=aact
[ "$platform" != windows ] || binary=aact.exe
[ -f "$work/tree/bin/$binary" ] && [ -d "$work/tree/packages" ] || { echo 'Release archive is incomplete' >&2; exit 1; }
release="$ROOT/releases/${VERSION}-${platform}-${arch}"
printf '%s\n' "$expected" > "$work/tree/.archive-sha256"
if [ -e "$release" ]; then
  [ -f "$release/.archive-sha256" ] && [ "$(cat "$release/.archive-sha256")" = "$expected" ] || { echo 'Existing version directory differs; refusing to replace referenced resources' >&2; exit 1; }
else mv "$work/tree" "$release"; fi
chmod +x "$release/bin/$binary"
if [ "$platform" = windows ]; then
  printf '#!/bin/sh\nexec "%s/bin/aact.exe" "$@"\n' "$release" > "$launcher"
  chmod +x "$launcher"
  mv -f "$launcher" "$ROOT/bin/aact"
  printf '@"%%~dp0..\\releases\\%s-windows-%s\\bin\\aact.exe" %%*\r\n' "$VERSION" "$arch" > "$ROOT/bin/aact.cmd"
else
  ln -s "$release/bin/aact" "$launcher"
  mv -f "$launcher" "$ROOT/bin/aact"
fi
printf 'Installed AACT %s (%s/%s) at %s\n' "$VERSION" "$platform" "$arch" "$release"
case ":${PATH:-}:" in *":$ROOT/bin:"*) ;; *) printf 'Add %s to PATH to run aact. Your shell profiles were not modified.\n' "$ROOT/bin" ;; esac
