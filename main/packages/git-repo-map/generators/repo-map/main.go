// repo-map is a native, read-only generator. It never contacts a Git server.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
)

type request struct {
	ProtocolVersion int `json:"protocol_version"`
	Inputs          struct {
		ScanRoots []string `json:"scan_roots"`
		Hosts     []string `json:"hosts"`
	} `json:"inputs"`
}

type result struct {
	Repositories []Repository `json:"repositories"`
}

func run(stdin io.Reader, stdout, stderr io.Writer) int {
	fail := func(message string) int { fmt.Fprintln(stderr, "repo-map:", message); return 1 }
	const maxRequest = 16 << 20
	input, err := io.ReadAll(io.LimitReader(stdin, maxRequest+1))
	if err != nil {
		return fail("cannot read generator request")
	}
	if len(input) > maxRequest {
		return fail("generator request exceeds 16 MiB")
	}
	var req request
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	// Extra context supplied by the manager is intentionally accepted.
	if err := decoder.Decode(&req); err != nil {
		return fail("invalid generator JSON or input types")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fail("expected exactly one JSON request")
	}
	if req.ProtocolVersion != 1 {
		return fail("unsupported protocol_version; expected 1")
	}
	if len(req.Inputs.ScanRoots) == 0 {
		return fail("scan_roots must contain at least one directory")
	}
	for _, root := range req.Inputs.ScanRoots {
		if strings.TrimSpace(root) == "" {
			return fail("scan_roots contains an empty directory")
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	fmt.Fprintf(stderr, "Scanning %d root(s) for local Git checkouts...\n", len(req.Inputs.ScanRoots))
	rows, err := Scan(ctx, req.Inputs.ScanRoots, req.Inputs.Hosts)
	if err != nil {
		return fail(err.Error())
	}
	output, err := json.Marshal(result{rows})
	if err != nil {
		return fail("cannot encode repository map")
	}
	if len(output) > 16<<20 {
		return fail("repository map exceeds 16 MiB")
	}
	fmt.Fprintf(stderr, "Found %d repository checkout row(s).\n", len(rows))
	output = append(output, '\n')
	if _, err := stdout.Write(output); err != nil {
		return fail("cannot write repository map")
	}
	return 0
}

func main() { os.Exit(run(os.Stdin, os.Stdout, os.Stderr)) }
