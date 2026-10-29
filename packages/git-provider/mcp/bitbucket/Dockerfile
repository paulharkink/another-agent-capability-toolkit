FROM ghcr.io/n11techhub/mcp-bitbucket@sha256:a2a53b5cbc47789eac4289177d42f63a592a5f3ca21294373a6a90e5d346c4bc AS bitbucket-upstream

# This compiled-client patch is based on upstream v2.1.2 source revision
# b65b2030d0b4e7363a1b011c8dd0b6b9d2c881d4. The SHA guard below pins the
# exact architecture-independent SearchClient artifact in the upstream image.
FROM bitbucket-upstream AS bitbucket-search-overlay
USER root
COPY search-client.patch /tmp/search-client.patch
COPY search-client.integration.test.mjs /tmp/search-client.integration.test.mjs
RUN printf '%s  %s\n' \
      'b8de632392665beefc37887cd8ebf29234be9a1c59dc5f58ef18ed36f1ab1ec5' \
      '/usr/src/app/dist/src/infrastructure/client/SearchClient.js' \
      | sha256sum -c - \
    && apk add --no-cache patch \
    && patch --batch --fuzz=0 -p1 -d /usr/src/app < /tmp/search-client.patch \
    && node --test /tmp/search-client.integration.test.mjs

FROM golang:1.27.1-alpine AS wrapper-build
WORKDIR /src
COPY handler.go main.go entrypoint.go child_process_unix.go child_process_windows.go ./
RUN mkdir -p /out && CGO_ENABLED=0 GO111MODULE=off go build -trimpath -ldflags="-s -w" -o /out/git-mcp-wrapper .

FROM bitbucket-upstream
COPY --from=bitbucket-search-overlay --chown=appuser:appgroup \
    /usr/src/app/dist/src/infrastructure/client/SearchClient.js \
    /usr/src/app/dist/src/infrastructure/client/SearchClient.js
COPY --from=wrapper-build /out/git-mcp-wrapper /usr/local/bin/git-mcp-wrapper
ENV MCP_HEALTH_PROVIDER=bitbucket \
    MCP_HEALTH_UPSTREAM=http://127.0.0.1:3001 \
    MCP_HEALTH_LISTEN=0.0.0.0:3002
ENTRYPOINT ["/usr/local/bin/git-mcp-wrapper"]
CMD ["docker-entrypoint.sh", "npm", "start"]
