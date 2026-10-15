#!/bin/sh
# Bootstrap only: installed Unix launchers point directly to the native binary.
set -eu
VERSION=${AACT_VERSION:-}
LOCAL_ARCHIVE=
BASE=${AACT_DOWNLOAD_BASE:-https://github.com/paulharkink/another-agent-capability-toolkit/releases/download}
ROOT=${AACT_INSTALL_DIR:-${HOME:?HOME is required}/.local/share/aact}
MIGRATE_USER_BIN=1
[ "${AACT_INSTALL_DIR+x}" != x ] || MIGRATE_USER_BIN=0
PRINT_TARGET=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    -v|--version) [ "$#" -ge 2 ] || { echo '-v/--version needs a value' >&2; exit 1; }; VERSION=$2; shift 2 ;;
    --local-archive) [ "$#" -ge 2 ] || { echo '--local-archive needs a path' >&2; exit 1; }; LOCAL_ARCHIVE=$2; shift 2 ;;
    --base-url) [ "$#" -ge 2 ] || { echo '--base-url needs a value' >&2; exit 1; }; BASE=$2; shift 2 ;;
    --install-dir) [ "$#" -ge 2 ] || { echo '--install-dir needs a value' >&2; exit 1; }; ROOT=$2; MIGRATE_USER_BIN=0; shift 2 ;;
    --print-target) PRINT_TARGET=1; shift ;;
    --help) echo 'Usage: install.sh (-v|--version VERSION | --local-archive ARCHIVE) [--install-dir ROOT] [--base-url URL] [--print-target]'; exit 0 ;;
    *) printf 'Unknown installer argument: %s\n' "$1" >&2; exit 1 ;;
  esac
done
system=${AACT_OS:-$(uname -s)}
case "$system" in Darwin|darwin|macos) platform=darwin ;; Linux|linux) platform=linux ;; MINGW*|MSYS*|CYGWIN*|Windows_NT|windows) platform=windows ;; *) printf 'Unsupported operating system: %s\n' "$system" >&2; exit 1 ;; esac
machine=${AACT_ARCH:-$(uname -m)}
case "$machine" in x86_64|amd64|AMD64|x64) arch=amd64 ;; aarch64|arm64|ARM64) arch=arm64 ;; *) printf 'Unsupported architecture: %s\n' "$machine" >&2; exit 1 ;; esac
if [ "$PRINT_TARGET" = 1 ]; then printf '%s_%s\n' "$platform" "$arch"; exit 0; fi
extension=tar.gz
[ "$platform" != windows ] || extension=zip
if [ -n "$LOCAL_ARCHIVE" ]; then
  archive_name=$(basename "$LOCAL_ARCHIVE")
  suffix="_${platform}_${arch}.${extension}"
  case "$archive_name" in
    aact_*"$suffix") ;;
    *) printf 'Local archive does not match this platform (%s/%s): %s\n' "$platform" "$arch" "$archive_name" >&2; exit 1 ;;
  esac
  archive_version=${archive_name#aact_}
  archive_version=${archive_version%"$suffix"}
  case "$archive_version" in ''|*[!A-Za-z0-9._-]*|.|..) echo 'Invalid release version in local archive name' >&2; exit 1 ;; esac
  if [ -n "$VERSION" ] && [ "$VERSION" != "$archive_version" ]; then
    printf 'Requested version %s does not match local archive version %s\n' "$VERSION" "$archive_version" >&2
    exit 1
  fi
  VERSION=$archive_version
else
  [ -n "$VERSION" ] || { echo 'Specify --version or --local-archive' >&2; exit 1; }
  case "$VERSION" in ''|*[!A-Za-z0-9._-]*|.|..) echo 'Invalid release version' >&2; exit 1 ;; esac
fi
filename="aact_${VERSION}_${platform}_${arch}.${extension}"
download() {
  if command -v curl >/dev/null 2>&1; then curl --fail --location --silent --show-error "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -q "$1" -O "$2"
  else echo 'curl or wget is required to download the release' >&2; return 1; fi
}
mkdir -p "$ROOT/releases" "$ROOT/bin"
ROOT=$(cd "$ROOT" && pwd)
ROOT_PHYSICAL=$(CDPATH= cd -P "$ROOT" && pwd -P)
work=$(mktemp -d "$ROOT/releases/.install-XXXXXX")
launcher="$ROOT/bin/.aact-next-$$"
trap 'rm -rf "$work"; rm -f "$launcher"' EXIT HUP INT TERM
if [ -n "$LOCAL_ARCHIVE" ]; then
  [ -f "$LOCAL_ARCHIVE" ] || { printf 'Local archive is not a regular file: %s\n' "$LOCAL_ARCHIVE" >&2; exit 1; }
  checksum_source="$(dirname "$LOCAL_ARCHIVE")/checksums.txt"
  [ -f "$checksum_source" ] || { printf 'Adjacent checksums.txt is missing: %s\n' "$checksum_source" >&2; exit 1; }
  cp "$LOCAL_ARCHIVE" "$work/$filename"
else
  download "${BASE%/}/v${VERSION}/${filename}" "$work/$filename"
  download "${BASE%/}/v${VERSION}/checksums.txt" "$work/checksum"
  checksum_source="$work/checksum"
fi
expected=$(awk -v name="$filename" '$2 == name { print $1; exit }' "$checksum_source")
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
if [ "$platform" != windows ]; then
  canonical="$ROOT/bin/aact"
  if [ -e "$canonical" ] || [ -L "$canonical" ]; then
    canonical_target=
    canonical_backup=
    if [ -L "$canonical" ]; then
      canonical_target=$(readlink -f "$canonical" 2>/dev/null || true)
      case "$canonical_target" in
        "$ROOT_PHYSICAL"/releases/*/bin/aact) ;;
        *) printf 'Canonical launcher is an unrelated symlink and was left unchanged: %s\n' "$canonical" >&2; exit 1 ;;
      esac
      canonical_release_dir=$(dirname "$(dirname "$canonical_target")")
      if [ ! -f "$canonical_release_dir/.archive-sha256" ]; then
        printf 'Canonical launcher target lacks AACT release metadata and was left unchanged: %s\n' "$canonical" >&2
        exit 1
      fi
      canonical_backup="$ROOT/legacy/bin/aact-canonical-managed-$(basename "$canonical_release_dir")"
    else
      if [ ! -f "$canonical" ] || [ ! -x "$canonical" ]; then
        printf 'Canonical launcher is not an executable regular file and was left unchanged: %s\n' "$canonical" >&2
        exit 1
      fi
      canonical_help=$("$canonical" --help 2>/dev/null || true)
      canonical_version=$("$canonical" --version 2>/dev/null || true)
      canonical_version=$(printf '%s\n' "$canonical_version" | sed -n '1p')
      if ! printf '%s' "$canonical_help" | grep -Fq 'Another Agent Capability Toolkit (aact)'; then
        printf 'Canonical launcher was not recognized as AACT and was left unchanged: %s\n' "$canonical" >&2
        exit 1
      fi
      case "$canonical_version" in
        [0-9]*|v[0-9]*) ;;
        *) printf 'Canonical launcher version was not recognized and was left unchanged: %s\n' "$canonical" >&2; exit 1 ;;
      esac
      case "$canonical_version" in *[!A-Za-z0-9._+-]*|'')
        printf 'Canonical launcher version is unsafe to back up: %s\n' "$canonical" >&2
        exit 1
        ;;
      esac
      canonical_backup="$ROOT/legacy/bin/aact-canonical-$canonical_version"
    fi
    if [ "$canonical_target" = "$ROOT_PHYSICAL/releases/${VERSION}-${platform}-${arch}/bin/aact" ]; then
      :
    elif [ -e "$canonical_backup" ] || [ -L "$canonical_backup" ]; then
      printf 'Cannot update canonical launcher; backup already exists: %s\n' "$canonical_backup" >&2
      exit 1
    else
      mkdir -p "$ROOT/legacy/bin"
      if ! cp -a "$canonical" "$canonical_backup"; then
        printf 'Could not back up canonical launcher: %s\n' "$canonical" >&2
        exit 1
      fi
      printf 'Backed up canonical launcher %s at %s\n' "$canonical" "$canonical_backup"
    fi
  fi
fi
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
if [ "$platform" != windows ] && [ "$MIGRATE_USER_BIN" = 1 ]; then
  legacy="$HOME/.local/bin/aact"
  if [ -L "$legacy" ]; then
    target=$(readlink "$legacy" 2>/dev/null || true)
    if [ "$target" != "$ROOT/bin/aact" ]; then
      printf 'Legacy launcher is a symlink and was left unchanged: %s\n' "$legacy"
    fi
  elif [ -e "$legacy" ]; then
    if [ -f "$legacy" ] && [ -x "$legacy" ]; then
      legacy_help=$("$legacy" --help 2>/dev/null || true)
      if ! printf '%s' "$legacy_help" | grep -Fq 'Another Agent Capability Toolkit (aact)'; then
        printf 'Legacy launcher was not recognized and was left unchanged: %s\n' "$legacy"
      else
      legacy_version=$("$legacy" --version 2>/dev/null || true)
      legacy_version=$(printf '%s\n' "$legacy_version" | sed -n '1p')
      case "$legacy_version" in
        [0-9]*|v[0-9]*) ;;
        *) printf 'Legacy launcher was not recognized and was left unchanged: %s\n' "$legacy" ;;
      esac
      case "$legacy_version" in
        [0-9]*|v[0-9]*)
          case "$legacy_version" in *[!A-Za-z0-9._+-]*|'')
            printf 'Legacy launcher version is unsafe to migrate; left unchanged: %s\n' "$legacy" ;;
            *)
              backup_dir="$ROOT/legacy/bin"
              backup="$backup_dir/aact-$legacy_version"
              if [ -e "$backup" ]; then
                printf 'Cannot migrate legacy launcher; backup already exists: %s\n' "$backup" >&2
                exit 1
              fi
              mkdir -p "$backup_dir"
              legacy_next="$legacy.migrating-$$"
              if mv "$legacy" "$backup" && ln -s "$ROOT/bin/aact" "$legacy_next" && mv -f "$legacy_next" "$legacy"; then
                printf 'Migrated legacy launcher %s (%s); original saved at %s\n' "$legacy" "$legacy_version" "$backup"
              else
                rm -f "$legacy_next"
                if [ -e "$backup" ] && [ ! -e "$legacy" ]; then mv "$backup" "$legacy"; fi
                printf 'Failed to migrate legacy launcher: %s\n' "$legacy" >&2
                exit 1
              fi
              ;;
          esac
          ;;
      esac
      fi
    else
      printf 'Legacy launcher is not an executable regular file and was left unchanged: %s\n' "$legacy"
    fi
  fi
fi
printf 'Installed AACT %s (%s/%s) at %s\n' "$VERSION" "$platform" "$arch" "$release"
case ":${PATH:-}:" in *":$ROOT/bin:"*) ;; *) printf 'Add %s to PATH to run aact. Your shell profiles were not modified.\n' "$ROOT/bin" ;; esac
