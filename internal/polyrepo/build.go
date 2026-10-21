package polyrepo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/process"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/render"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

const CourierForkURL = "https://github.com/paulharkink/opencode-courier.git"
// CourierForkCommit is updated to the reviewed pushed integration commit
// before this branch is opened as a PR. Until then validation uses the explicit
// --courier-source private worktree override.
const CourierForkCommit = "691385502885cdfb53879b1ff26aa12902104951"

// BuildCourier stages a pinned fork (or explicit local development checkout)
// below AACT state, runs its package build there, and publishes an immutable
// generated entrypoint. It never edits the supplied checkout.
func BuildCourier(ctx context.Context, stateRoot, localSource string, runner process.Executor, onStderr func([]byte)) (entry, revision string, err error) {
	if runner == nil {
		runner = process.OSExecutor{}
	}
	root := filepath.Join(stateRoot, "manager", "plugins", "polyrepo-coordinator")
	builds := filepath.Join(root, "builds")
	if err = os.MkdirAll(builds, 0700); err != nil {
		return "", "", err
	}
	stage, err := os.MkdirTemp(root, ".aact-build-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(stage)
	source := filepath.Join(stage, "source")
	if localSource != "" {
		localSource, err = filepath.Abs(localSource)
		if err != nil {
			return "", "", err
		}
		err = render.CopyTree(ctx, localSource, source)
		if err != nil {
			return "", "", err
		}
		out, _ := runner.Run(ctx, []string{"git", "-C", localSource, "rev-parse", "HEAD"}, localSource, nil, nil, nil)
		revision = strings.TrimSpace(string(out))
		if revision == "" {
			revision = "local-unversioned"
		}
	} else {
		if _, err = runner.Run(ctx, []string{"git", "clone", "--no-checkout", CourierForkURL, source}, stage, nil, nil, onStderr); err != nil {
			return "", "", fmt.Errorf("clone pinned Courier fork: %w", err)
		}
		if _, err = runner.Run(ctx, []string{"git", "-C", source, "checkout", "--detach", CourierForkCommit}, stage, nil, nil, onStderr); err != nil {
			return "", "", fmt.Errorf("checkout pinned Courier revision: %w", err)
		}
		revision = CourierForkCommit
	}
	if _, err = os.Stat(filepath.Join(source, "package.json")); err != nil {
		return "", "", fmt.Errorf("Courier source must contain package.json: %w", err)
	}
	if _, err = runner.Run(ctx, []string{"bun", "install", "--frozen-lockfile"}, source, nil, nil, onStderr); err != nil {
		return "", "", fmt.Errorf("Courier dependency setup: %w", err)
	}
	if _, err = runner.Run(ctx, []string{"bun", "run", "build"}, source, nil, nil, onStderr); err != nil {
		return "", "", fmt.Errorf("Courier build: %w", err)
	}
	dist := filepath.Join(source, "dist")
	if _, err = os.Stat(filepath.Join(dist, "index.js")); err != nil {
		return "", "", fmt.Errorf("Courier build did not produce dist/index.js: %w", err)
	}
	id := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	destination := filepath.Join(builds, id)
	if err = os.Rename(dist, destination); err != nil {
		return "", "", fmt.Errorf("stage built Courier output: %w", err)
	}
	if err = state.WriteJSON(filepath.Join(root, "source.json"), map[string]string{"source": localSource, "repository": CourierForkURL, "revision": revision, "build_id": id}); err != nil {
		return "", "", err
	}
	return filepath.Join(destination, "index.js"), revision, nil
}
