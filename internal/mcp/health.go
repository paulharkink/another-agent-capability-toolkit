package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func Health(ctx context.Context, url, transport string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	method := "POST"
	var body io.Reader = bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"aact","version":"0.1"}}}`)
	if transport == "sse" {
		method = "GET"
		body = nil
	}
	req, e := http.NewRequestWithContext(ctx, method, url, body)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("MCP health HTTP %d", resp.StatusCode)
	}
	if transport == "sse" {
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			return errors.New("MCP SSE content type missing")
		}
		return nil
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return e
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "data:") {
				b = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
				break
			}
		}
	}
	var v struct {
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	if e = json.Unmarshal(b, &v); e != nil || v.Result == nil || v.Result["protocolVersion"] == nil {
		return errors.New("MCP initialize returned invalid response")
	}
	return nil
}
