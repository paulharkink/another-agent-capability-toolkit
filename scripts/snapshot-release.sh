#!/bin/sh
set -eu

branch_slug() {
  printf '%s' "$1" | LC_ALL=C tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//'
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

branch=$(source_branch) || exit 1
slug=$(branch_slug "$branch")
[ -n "$slug" ] || {
  echo 'Snapshot branch name contains no SemVer-safe characters.' >&2
  exit 1
}
export AACT_SNAPSHOT_BRANCH=$slug
exec goreleaser release --snapshot "$@"
