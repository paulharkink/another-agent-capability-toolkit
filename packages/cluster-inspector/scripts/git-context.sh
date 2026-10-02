#!/usr/bin/env bash

set -euo pipefail

REPOSITORY_ONLY=false
if [ "${1:-}" = "--repository-only" ]; then
  REPOSITORY_ONLY=true
  shift
fi
if [ "$#" -ne 1 ]; then
  printf 'Usage: %s [--repository-only] <repository-path>\n' "$0" >&2
  exit 1
fi

REPOSITORY_PATH="$1"
ROOT="$(git -C "$REPOSITORY_PATH" rev-parse --show-toplevel 2>/dev/null)" || {
  printf 'Not inside a Git worktree: %s\n' "$REPOSITORY_PATH" >&2
  exit 1
}

REPOSITORY="$(basename "$ROOT")"
printf 'repository=%s\nproject_fragment=%s\n' "$REPOSITORY" "$REPOSITORY"
if [ "$REPOSITORY_ONLY" = false ]; then
  BRANCH="$(git -C "$ROOT" branch --show-current)"
  if [ -z "$BRANCH" ]; then
    printf '%s\n' 'No current Git branch is available.' >&2
    exit 1
  fi
  printf 'branch=%s\n' "$BRANCH"
fi
