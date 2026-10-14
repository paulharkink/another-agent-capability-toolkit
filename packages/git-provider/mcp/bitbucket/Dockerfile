FROM golang:1.27.1-alpine AS wrapper-build
WORKDIR /src
COPY handler.go main.go entrypoint.go child_process_unix.go child_process_windows.go ./
RUN mkdir -p /out && CGO_ENABLED=0 GO111MODULE=off go build -trimpath -ldflags="-s -w" -o /out/git-mcp-wrapper .

FROM ghcr.io/n11techhub/mcp-bitbucket@sha256:a2a53b5cbc47789eac4289177d42f63a592a5f3ca21294373a6a90e5d346c4bc
COPY --from=wrapper-build /out/git-mcp-wrapper /usr/local/bin/git-mcp-wrapper
ENV MCP_HEALTH_PROVIDER=bitbucket \
    MCP_HEALTH_UPSTREAM=http://127.0.0.1:3001 \
    MCP_HEALTH_LISTEN=0.0.0.0:3002
ENTRYPOINT ["/usr/local/bin/git-mcp-wrapper"]
CMD ["docker-entrypoint.sh", "npm", "start"]
