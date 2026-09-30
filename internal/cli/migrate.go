package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
	"io"
)

func runMigration(ctx context.Context, src config.Source, store *state.Store, apply, jsonOutput bool, out, errOut io.Writer) int {
	plan, e := state.PlanMigration(ctx, src, store)
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 1
	}
	if jsonOutput {
		if e = json.NewEncoder(out).Encode(plan); e != nil {
			fmt.Fprintln(errOut, e)
			return 1
		}
	} else {
		fmt.Fprintf(out, "%d skill registrations, %d auth directories, %d conflicts\n", len(plan.Changes), len(plan.Auth), len(plan.Conflicts))
		for _, r := range plan.Changes {
			fmt.Fprintf(out, "adopt %s: %s\n", r.Key.Package, r.Destination)
		}
		for _, a := range plan.Auth {
			fmt.Fprintf(out, "adopt auth %s/%s/%s: %s\n", a.Key.Package, a.Key.Environment, a.Key.Target, a.Path)
		}
		for _, conflict := range plan.Conflicts {
			fmt.Fprintln(out, "conflict:", conflict)
		}
	}
	if apply {
		if e = state.ApplyMigration(ctx, plan); e != nil {
			fmt.Fprintln(errOut, e)
			return 1
		}
		if !jsonOutput {
			fmt.Fprintln(out, "Migration metadata applied.")
		}
	} else if !jsonOutput {
		fmt.Fprintln(out, "Dry run: no files changed.")
	}
	return 0
}
