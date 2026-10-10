#!/usr/bin/env bash
set -euo pipefail

release_tag="${AACT_RELEASE_TAG:?AACT_RELEASE_TAG must be set to the release tag}"
if [[ ! "$release_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Invalid release tag: ${release_tag}" >&2
  exit 1
fi

repository="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set}"
repository_lower="$(printf '%s' "$repository" | tr '[:upper:]' '[:lower:]')"
repository_name="${repository#*/}"
manifest_file="$(mktemp)"
error_file="$(mktemp)"
trap 'rm -f "$manifest_file" "$error_file"' EXIT

for package in azure-inspector cluster-inspector grafana-inspector; do
  package_name="${package}-mcp"
  image="ghcr.io/${repository_lower}/${package_name}:${release_tag}"
  encoded_package="${repository_name}%2F${package_name}"

  if ! metadata="$(gh api "/user/packages/container/${encoded_package}" 2>"$error_file")"; then
    echo "Could not inspect GHCR visibility for ${image}." >&2
    cat "$error_file" >&2
    exit 1
  fi
  visibility="$(jq -r '.visibility // empty' <<<"$metadata")"
  if [[ "$visibility" != public ]]; then
    echo "GHCR package ${package_name} has visibility '${visibility:-unknown}', not public." >&2
    exit 1
  fi

  if ! docker buildx imagetools inspect "$image" --raw >"$manifest_file" 2>"$error_file"; then
    echo "Could not inspect existing GHCR image ${image}." >&2
    cat "$error_file" >&2
    exit 1
  fi
  if ! jq -e '
    type == "object" and (.manifests | type == "array") and
    ([.manifests[] | select(.platform.os == "linux" and .platform.architecture == "amd64")] | length == 1) and
    ([.manifests[] | select(.platform.os == "linux" and .platform.architecture == "arm64")] | length == 1)
  ' "$manifest_file" >/dev/null; then
    echo "Existing GHCR image ${image} must contain exactly one linux/amd64 and one linux/arm64 manifest." >&2
    exit 1
  fi

  echo "Verified public ${image} with linux/amd64 and linux/arm64 manifests."
done
