package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckConnectionReportsMCPResultAndTime(t *testing.T) {
	svc, _, _ := fixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`))
	}))
	defer server.Close()
	good := svc.CheckConnection(context.Background(), server.URL, "streamable-http")
	if !good.Reachable || good.CheckedAt.IsZero() || good.Error != "" {
		t.Fatalf("reachable MCP misreported: %+v", good)
	}
	bad := svc.CheckConnection(context.Background(), "http://127.0.0.1:1/mcp", "streamable-http")
	if bad.Reachable || bad.CheckedAt.IsZero() || bad.Error == "" {
		t.Fatalf("unreachable MCP failure hidden: %+v", bad)
	}
}
