FROM golang:1.27.1-alpine AS wrapper-build
WORKDIR /src
COPY handler.go main.go entrypoint.go child_process_unix.go child_process_windows.go ./
RUN mkdir -p /out && CGO_ENABLED=0 GO111MODULE=off go build -trimpath -ldflags="-s -w" -o /out/git-mcp-wrapper .

FROM zereight050/gitlab-mcp@sha256:5de479d912f76326b09da36570474555746eeb319405cfa731226aafe5552ab6
COPY --from=wrapper-build /out/git-mcp-wrapper /usr/local/bin/git-mcp-wrapper
ENV MCP_HEALTH_PROVIDER=gitlab \
    MCP_HEALTH_UPSTREAM=http://127.0.0.1:3002 \
    MCP_HEALTH_LISTEN=0.0.0.0:3003
ENTRYPOINT ["/usr/local/bin/git-mcp-wrapper"]
CMD ["node", "build/index.js"]
