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
done
docker build -t aact/find-session:test-runtime packages/find-session/container
docker build -f packages/find-session/container/Dockerfile.test --build-arg RUNTIME_IMAGE=aact/find-session:test-runtime -t aact/find-session:test packages/find-session/container
docker run --rm -v "$root/packages/find-session/container:/port:ro" aact/find-session:test
