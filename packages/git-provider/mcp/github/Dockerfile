FROM golang:1.27.1-alpine AS wrapper-build
WORKDIR /src
COPY handler.go main.go entrypoint.go ./
RUN mkdir -p /out && CGO_ENABLED=0 GO111MODULE=off go build -trimpath -ldflags="-s -w" -o /out/git-mcp-wrapper .

FROM ghcr.io/github/github-mcp-server@sha256:1817b57d43916532dc002bdc5f344d639bd9fb54a9148d42168458f7c3280567
COPY --from=wrapper-build /out/git-mcp-wrapper /usr/local/bin/git-mcp-wrapper
ENV MCP_HEALTH_PROVIDER=github \
    MCP_HEALTH_CHILD=/server/github-mcp-server \
    MCP_HEALTH_UPSTREAM=http://127.0.0.1:8082 \
    MCP_HEALTH_LISTEN=0.0.0.0:8084
ENTRYPOINT ["/usr/local/bin/git-mcp-wrapper"]
CMD ["/server/github-mcp-server", "http", "--port", "8082"]
