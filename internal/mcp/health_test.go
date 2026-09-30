package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthUsesConfiguredHostAndTransport(t *testing.T) {
	for _, transport := range []string{"streamable-http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/custom" {
					t.Error(r.URL.Path)
				}
				if transport == "sse" {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Write([]byte("event: endpoint\ndata: /messages\n\n"))
					return
				}
				if r.Method != "POST" {
					t.Error(r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fixture","version":"1"}}}`))
			}))
			defer s.Close()
			if e := Health(context.Background(), s.URL+"/custom", transport); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestHealthRejectsNonMCP(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer s.Close()
	e := Health(context.Background(), s.URL, "streamable-http")
	if e == nil || !strings.Contains(e.Error(), "MCP") {
		t.Fatal(e)
	}
}
