#!/usr/bin/env bash
set -euo pipefail

version="${GITHUB_REF_NAME#v}"
manifest_dir=$(mktemp -d)
trap 'rm -rf "$manifest_dir"' EXIT

report_retry() {
  local ref="$1" attempt="$2" error_file="$3"
  echo "Registry inspection for ${ref} did not produce a usable manifest (attempt ${attempt}/5)." >&2
  if [[ -s "$error_file" ]]; then cat "$error_file" >&2; fi
}

# Return 0 for a valid manifest, 2 when an initial probe finds no tag, and 1
# after bounded retries for any other registry/read-after-write failure.
inspect_raw_with_retry() {
  local ref="$1" output_file="$2" error_file="$3" retry_missing="$4"
  local attempt delay
  for attempt in 1 2 3 4 5; do
    if docker buildx imagetools inspect "$ref" --raw >"$output_file" 2>"$error_file" &&
      jq -e 'type == "object" and (.manifests | type == "array")' "$output_file" >/dev/null; then
      return 0
    fi
    if [[ "$retry_missing" != true ]] && grep -Eiq 'not found|manifest unknown|name unknown' "$error_file"; then
      return 2
    fi
    report_retry "$ref" "$attempt" "$error_file"
    if [[ "$attempt" -lt 5 ]]; then
      delay=$((attempt * 2))
      echo "Retrying registry inspection for ${ref} in ${delay}s." >&2
      sleep "$delay"
    fi
  done
  echo "Failed to inspect ${ref} after 5 attempts; refusing to publish an unverified manifest." >&2
  return 1
}

source_platform_digest() {
  local ref="$1" arch="$2" output_file="$3" error_file="$4"
  if ! inspect_raw_with_retry "$ref" "$output_file" "$error_file" true; then
    echo "Could not read the ${arch} staging manifest at ${ref}." >&2
    return 1
  fi
  jq -er --arg arch "$arch" \
    '[.manifests[] | select(.platform.os == "linux" and .platform.architecture == $arch) | .digest] | if length == 1 then .[0] else error("expected exactly one linux/" + $arch + " descriptor") end' \
    "$output_file"
}

verify_final_manifest() {
  local ref="$1" manifest_file="$2" amd64_digest="$3" arm64_digest="$4"
  if ! jq -e --arg amd64 "$amd64_digest" --arg arm64 "$arm64_digest" '
    [.manifests[] | select(.platform.os == "linux" and .platform.architecture == "amd64") | .digest] == [$amd64] and
    [.manifests[] | select(.platform.os == "linux" and .platform.architecture == "arm64") | .digest] == [$arm64]
  ' "$manifest_file" >/dev/null; then
    echo "${ref}: existing image does not match the architecture digests from this run; refusing to overwrite it." >&2
    return 1
  fi
}

package_has_tag() {
  local image="$1" tag="$2" package encoded_package tags
  package="${image#ghcr.io/${GITHUB_REPOSITORY,,}/}"
  encoded_package="${GITHUB_REPOSITORY#*/}%2F${package}"
  if ! tags=$(gh api --paginate "/user/packages/container/${encoded_package}/versions" --jq '.[]?.metadata.container.tags[]?' 2>"$manifest_dir/package-api.err"); then
    echo "Could not check GHCR package metadata for ${image}; refusing to assume ${tag} is absent." >&2
    cat "$manifest_dir/package-api.err" >&2
    return 2
  fi
  if grep -Fqx "$tag" <<<"$tags"; then return 0; fi
  return 1
}

capture_digest_with_retry() {
  local ref="$1" error_file="$2" attempt delay output digest
  for attempt in 1 2 3 4 5; do
    if output=$(docker buildx imagetools inspect "$ref" 2>"$error_file"); then
      digest=$(awk '/^Digest:/ { print $2; exit }' <<<"$output")
      if [[ "$digest" == sha256:* ]]; then
        printf '%s\n' "$digest"
        return 0
      fi
      echo "Registry inspection for ${ref} omitted its digest (attempt ${attempt}/5)." >&2
    else
      report_retry "$ref" "$attempt" "$error_file"
    fi
    if [[ "$attempt" -lt 5 ]]; then
      delay=$((attempt * 2))
      echo "Retrying digest capture for ${ref} in ${delay}s." >&2
      sleep "$delay"
    fi
  done
  echo "Failed to capture the registry digest for ${ref} after 5 attempts." >&2
  return 1
}

mkdir -p image-digests/final
for arm64_file in image-digests/arm64/*.txt; do
  package="$(basename "$arm64_file" .txt)"
  amd64_file="image-digests/amd64/${package}.txt"
  test -f "$amd64_file"
  read -r image amd64_staging amd64_digest < "$amd64_file"
  read -r arm64_image arm64_staging arm64_digest < "$arm64_file"
  test "$image" = "$arm64_image"

  amd64_source="${image}@${amd64_digest}"
  arm64_source="${image}@${arm64_digest}"
  amd64_platform_digest="$(source_platform_digest "$amd64_source" amd64 "$manifest_dir/amd64.json" "$manifest_dir/inspect.err")"
  arm64_platform_digest="$(source_platform_digest "$arm64_source" arm64 "$manifest_dir/arm64.json" "$manifest_dir/inspect.err")"
  versioned_image="${image}:v${version}"
  final_manifest="$manifest_dir/${package}-final.json"

  tag_exists=false
  if inspect_raw_with_retry "$versioned_image" "$final_manifest" "$manifest_dir/inspect.err" false; then
    tag_exists=true
  else
    inspect_rc=$?
    if [[ "$inspect_rc" -eq 2 ]]; then
      if package_has_tag "$image" "v${version}"; then
        echo "GHCR package metadata lists ${versioned_image}; waiting for the registry manifest to become readable."
        if ! inspect_raw_with_retry "$versioned_image" "$final_manifest" "$manifest_dir/inspect.err" true; then
          echo "GHCR metadata lists ${versioned_image}, but its manifest remained unreadable." >&2
          exit 1
        fi
        tag_exists=true
      else
        metadata_rc=$?
        if [[ "$metadata_rc" -ne 1 ]]; then
          echo "Could not safely determine whether ${versioned_image} exists; refusing to overwrite it." >&2
          exit 1
        fi
      fi
    else
      echo "Could not safely determine whether ${versioned_image} exists; refusing to overwrite it." >&2
      exit 1
    fi
  fi

  if [[ "$tag_exists" == true ]]; then
    verify_final_manifest "$versioned_image" "$final_manifest" "$amd64_platform_digest" "$arm64_platform_digest"
    echo "Skipping existing version-pinned image ${versioned_image}; both platform digests match this run."
  else
    create_rc=0
    docker buildx imagetools create \
      --tag "$versioned_image" \
      "$amd64_source" \
      "$arm64_source" || create_rc=$?
    if [[ "$create_rc" -ne 0 ]]; then
      echo "imagetools create returned ${create_rc} for ${versioned_image}; checking whether the registry committed the manifest before deciding the result." >&2
    fi

    if ! inspect_raw_with_retry "$versioned_image" "$final_manifest" "$manifest_dir/inspect.err" true; then
      echo "Failed to verify the published manifest ${versioned_image} after imagetools create (exit ${create_rc})." >&2
      exit 1
    fi
    verify_final_manifest "$versioned_image" "$final_manifest" "$amd64_platform_digest" "$arm64_platform_digest"
  fi

  final_digest="$(capture_digest_with_retry "$versioned_image" "$manifest_dir/inspect.err")"
  printf '%s %s\n' "$versioned_image" "$final_digest" > "image-digests/final/${package}.txt"
  echo "Verified ${versioned_image} (${final_digest}) for linux/amd64 and linux/arm64."
done
