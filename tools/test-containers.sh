#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

build_image() {
  dockerfile=
  want_file=0
  last_arg=
  for arg in "$@"; do
    if [ "$want_file" -eq 1 ]; then
      dockerfile=$arg
      want_file=0
    elif [ "$arg" = "-f" ]; then
      want_file=1
    fi
    last_arg=$arg
  done
  if [ -z "$dockerfile" ]; then
    dockerfile="$last_arg/Dockerfile"
  fi
  default_base=$(sed -n 's/^ARG PYTHON_BASE_IMAGE=//p' "$dockerfile")
  case "$default_base" in
    *python:3.13-slim@sha256:*)
      base=${AACT_PYTHON_BASE_IMAGE_3_13_SLIM:-$default_base}
      docker build --build-arg "PYTHON_BASE_IMAGE=$base" "$@"
      ;;
    *python:3.12-slim@sha256:*)
      base=${AACT_PYTHON_BASE_IMAGE_3_12_SLIM:-$default_base}
      docker build --build-arg "PYTHON_BASE_IMAGE=$base" "$@"
      ;;
    *) docker build "$@" ;;
  esac
}

# These images and tests use only synthetic local credentials/fixtures.
build_image -f packages/_shared/Dockerfile.test -t aact/shared:test packages/_shared
docker run --rm -v "$root/packages/_shared:/port:ro" aact/shared:test
builds=$(go run ./cmd/mcp-release-builds -root "$root" -version test)
while IFS="$(printf '\t')" read -r package profile context release_image; do
  [ -n "$package" ] || continue
  build_image -f "$context/Dockerfile" -t "aact/$package-$profile:test-release" "$context"
done <<EOF_BUILDS
$(printf '%s\n' "$builds" | jq -r '.[] | [.package_id, .profile, .build_context, .release_image] | @tsv')
EOF_BUILDS
for package in cluster-inspector grafana-inspector; do
  build_image -t "aact/$package:test-runtime" "packages/$package/mcp"
  build_image -f "packages/$package/mcp/Dockerfile.test" --build-arg "RUNTIME_IMAGE=aact/$package:test-runtime" -t "aact/$package:test" "packages/$package/mcp"
  docker run --rm -v "$root/packages/$package/mcp:/port:ro" "aact/$package:test"
done
for package in azure-inspector; do
  build_image -f "packages/$package/mcp/Dockerfile.test" -t "aact/$package:test" "packages/$package/mcp"
  docker run --rm -v "$root/packages/$package/mcp:/port:ro" "aact/$package:test"
  build_image -t "aact/$package:release-test" "packages/$package/mcp"
  version=$(docker run --rm "aact/$package:release-test" --version)
  test "$version" = "0.0.12"
  name="aact-azure-sse-smoke-$$"
  response=$(mktemp)
  docker run --rm -d --name "$name" -p 127.0.0.1:18084:8084 \
    -e AZMCP_TRANSPORT=sse -e AZMCP_PORT=8084 "aact/$package:release-test" server start >/dev/null
  trap 'docker rm -f "$name" >/dev/null 2>&1 || true; rm -f "$response"' EXIT
  ready=0
  for attempt in $(seq 1 30); do
    if curl --max-time 3 -sS "http://127.0.0.1:18084/sse" >"$response" 2>/dev/null; then
      ready=1
      break
    fi
    if grep -q 'event: endpoint' "$response"; then
      ready=1
      break
    fi
    sleep 1
  done
  grep -q 'event: endpoint' "$response"
  test "$ready" = 1
  docker rm -f "$name" >/dev/null
  rm -f "$response"
  trap - EXIT
done
build_image -t aact/find-session:test-runtime packages/find-session/container
build_image -f packages/find-session/container/Dockerfile.test --build-arg RUNTIME_IMAGE=aact/find-session:test-runtime -t aact/find-session:test packages/find-session/container
docker run --rm -v "$root/packages/find-session/container:/port:ro" aact/find-session:test
