#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
# These images and tests use only synthetic local credentials/fixtures.
docker build -f packages/_shared/Dockerfile.test -t aact/shared:test packages/_shared
docker run --rm -v "$root/packages/_shared:/port:ro" aact/shared:test
for package in cluster-inspector grafana-inspector; do
  docker build -t "aact/$package:test-runtime" "packages/$package/mcp"
  docker build -f "packages/$package/mcp/Dockerfile.test" --build-arg "RUNTIME_IMAGE=aact/$package:test-runtime" -t "aact/$package:test" "packages/$package/mcp"
  docker run --rm -v "$root/packages/$package/mcp:/port:ro" "aact/$package:test"
done
for package in azure-inspector; do
  docker build -f "packages/$package/mcp/Dockerfile.test" -t "aact/$package:test" "packages/$package/mcp"
  docker run --rm -v "$root/packages/$package/mcp:/port:ro" "aact/$package:test"
  docker build -t "aact/$package:release-test" "packages/$package/mcp"
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
docker build -t aact/find-session:test-runtime packages/find-session/container
docker build -f packages/find-session/container/Dockerfile.test --build-arg RUNTIME_IMAGE=aact/find-session:test-runtime -t aact/find-session:test packages/find-session/container
docker run --rm -v "$root/packages/find-session/container:/port:ro" aact/find-session:test
