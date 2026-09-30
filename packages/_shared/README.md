# Shared inspector resources

kubernetes_auth.py and its preservation tests were ported from the approved agent-skills project. Cluster Inspector includes an identical copy in its self-contained Docker build context. Keep both copies synchronized. Authentication state and selected configuration belong in manager-owned target state; no credential values or target endpoints are bundled here.

Dockerfile.test defines development dependencies only. Run tests in containers:

```sh
docker build -t aact/cluster-inspector:test-runtime packages/cluster-inspector/mcp
docker build -f packages/cluster-inspector/mcp/Dockerfile.test -t aact/cluster-tests:local packages/cluster-inspector/mcp
docker run --rm --network none -v "$PWD/packages/cluster-inspector/mcp:/port:ro" aact/cluster-tests:local tests -q -p no:cacheprovider

docker build -t aact/grafana-inspector:test-runtime packages/grafana-inspector/mcp
docker build -f packages/grafana-inspector/mcp/Dockerfile.test -t aact/grafana-tests:local packages/grafana-inspector/mcp
docker run --rm --network none -v "$PWD/packages/grafana-inspector/mcp:/port:ro" aact/grafana-tests:local tests -q -p no:cacheprovider

docker build -f packages/_shared/Dockerfile.test -t aact/shared-tests:local packages/_shared
docker run --rm --network none -v "$PWD/packages/_shared:/port:ro" aact/shared-tests:local

docker build -f packages/azure-inspector/mcp/Dockerfile.test -t aact/config-tests:local packages/azure-inspector/mcp
docker run --rm --network none -v "$PWD/packages/azure-inspector/mcp:/port:ro" aact/config-tests:local
docker run --rm --network none -v "$PWD/packages/forgejo/mcp:/port:ro" aact/config-tests:local

docker build -t aact/find-session:test-runtime packages/find-session/container
docker build -f packages/find-session/container/Dockerfile.test -t aact/session-tests:local packages/find-session/container
docker run --rm --network none -v "$PWD/packages/find-session/container:/port:ro" aact/session-tests:local

go test ./tests/packages
go test -tags docker_integration ./tests/packages
```

The opt-in Go suite builds its own Azure and Forgejo resources and checks actual published endpoints against synthetic fixtures. It creates/removes only containers bearing unique validation names.
