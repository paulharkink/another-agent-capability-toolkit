#!/bin/sh
set -eu

branch_slug() {
  printf '%s' "$1" | LC_ALL=C tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//'
}

release_image_version() {
  if git remote get-url origin >/dev/null 2>&1; then
    git fetch --quiet --tags origin || {
      echo 'Unable to fetch all release tags from origin; refusing to derive a potentially stale snapshot version.' >&2
      return 1
    }
  fi
  tag=$(git tag --list 'v[0-9]*' --sort=-version:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sed -n '1p')
  [ -n "$tag" ] || {
    echo 'Snapshot build requires a stable vMAJOR.MINOR.PATCH tag for release image pinning.' >&2
    return 1
  }
  printf '%s\n' "${tag#v}"
}

source_branch() {
  if [ -n "${AACT_SNAPSHOT_SOURCE_BRANCH:-}" ]; then
    printf '%s\n' "$AACT_SNAPSHOT_SOURCE_BRANCH"
    return
  fi
  git symbolic-ref --quiet --short HEAD || {
    echo 'Snapshot version requires an attached branch or AACT_SNAPSHOT_SOURCE_BRANCH; detached HEAD builds are not supported.' >&2
    return 1
  }
}

if [ "${1:-}" = "--print-branch" ]; then
  shift
  if [ "$#" -eq 1 ]; then
    branch=$1
  elif [ "$#" -eq 0 ]; then
    branch=$(source_branch) || exit 1
  else
    echo 'Usage: snapshot-release.sh --print-branch [BRANCH]' >&2
    exit 2
  fi
  slug=$(branch_slug "$branch")
  [ -n "$slug" ] || {
    echo 'Snapshot branch name contains no SemVer-safe characters.' >&2
    exit 1
  }
  printf '%s\n' "$slug"
  exit 0
fi

if [ "${1:-}" = "--print-image-version" ]; then
  [ "$#" -eq 1 ] || {
    echo 'Usage: snapshot-release.sh --print-image-version' >&2
    exit 2
  }
  release_image_version
  exit $?
fi

branch=$(source_branch) || exit 1
slug=$(branch_slug "$branch")
[ -n "$slug" ] || {
  echo 'Snapshot branch name contains no SemVer-safe characters.' >&2
  exit 1
}
export AACT_SNAPSHOT_BRANCH=$slug
AACT_RELEASE_IMAGE_VERSION=$(release_image_version) || exit 1
export AACT_RELEASE_IMAGE_VERSION
exec goreleaser release --snapshot "$@"
