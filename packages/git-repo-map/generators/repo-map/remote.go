package main

import "github.com/paulharkink/another-agent-capability-toolkit/internal/repomap"

func NormalizeRemote(remote string) (string, string, error) { return repomap.NormalizeRemote(remote) }
