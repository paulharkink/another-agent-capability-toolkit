package mcp

import (
	"bufio"
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
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		var data []string
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if initializeResponse([]byte(strings.Join(data, "\n"))) {
					return nil
				}
				data = nil
			} else if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		return errors.New("MCP initialize returned invalid response")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return e
	}
	if !initializeResponse(b) {
		return errors.New("MCP initialize returned invalid response")
	}
	return nil
}

func initializeResponse(b []byte) bool {
	var v struct {
		ID     json.RawMessage `json:"id"`
		Result map[string]any  `json:"result"`
		Error  any             `json:"error"`
	}
	return json.Unmarshal(b, &v) == nil && string(v.ID) == "1" && v.Error == nil && v.Result != nil && v.Result["protocolVersion"] != nil
}
