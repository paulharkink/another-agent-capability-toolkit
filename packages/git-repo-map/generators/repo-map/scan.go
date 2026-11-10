package main

import (
	"context"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/repomap"
)

type Repository = repomap.Repository

func Scan(ctx context.Context, roots, hosts []string) ([]Repository, error) {
	return repomap.Scan(ctx, roots, hosts)
}
