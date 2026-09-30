package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestStreamableHealthAcceptsInitializeWithoutWaitingForSSEClose(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n"))
		w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"protocolVersion\":\"other\"}}\n\n"))
		w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":1,\ndata: \"result\":{\"protocolVersion\":\"2024-11-05\",\"capabilities\":{}}}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := Health(ctx, s.URL, "streamable-http"); err != nil {
		t.Fatalf("valid open SSE response rejected: %v", err)
	}
}
