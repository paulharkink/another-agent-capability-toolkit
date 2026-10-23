package app

import (
	"context"
	"fmt"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

// reconcileRuntimeLedger removes only local manager metadata for runtime rows
// that cannot be linked to a declared MCP under any visible instance of this
// capability. It never mutates Docker or agent registrations.
// Rows for declared MCPs are retained even if their runtime is missing. When
// Docker observes the same ID under a different declared key, the ledger row
// is rekeyed to keep the visible runtime manageable. Unlinked rows are removed
// as metadata only; this function never mutates Docker or agent registrations.
func (s *Service) reconcileRuntimeLedger(ctx context.Context, p catalog.Package, instances []config.Instance, observed []mcp.Instance, observationAvailable bool) error {
	return s.Store.WithLock(ctx, func() error {
		return s.reconcileRuntimeLedgerLocked(p, instances, observed, observationAvailable)
	})
}

func (s *Service) reconcileRuntimeLedgerLocked(p catalog.Package, instances []config.Instance, observed []mcp.Instance, observationAvailable bool) error {
	rows, err := s.Store.Installations()
	if err != nil {
		return err
	}
	known := make(map[state.Key]struct{}, len(instances)*len(p.MCPDefinitions()))
	for _, instance := range instances {
		base, err := s.Store.ResolveInstanceKey(s.Source.ID, p.ID, instance.Ref.Name)
		if err != nil {
			// Ambiguous identity is not evidence that any row is orphaned.
			return fmt.Errorf("cannot resolve instance %q safely: %w", instance.Ref.Name, err)
		}
		for _, definition := range p.MCPDefinitions() {
			known[mcpDefinitionKey(base, p, definition)] = struct{}{}
		}
	}
	observedByID := make(map[string][]mcp.Instance)
	if observationAvailable {
		for _, item := range observed {
			if item.ID == "" {
				continue
			}
			observedByID[item.ID] = append(observedByID[item.ID], item)
		}
	}
	for _, row := range rows {
		if row.Component != "runtime" || row.Key.Source != s.Source.ID || row.Key.Package != p.ID {
			continue
		}
		observedItems := observedByID[row.SourcePath]
		if observationAvailable && len(observedItems) > 0 {
			exactKeyObserved := false
			for _, item := range observedItems {
				if item.Key == row.Key {
					exactKeyObserved = true
					break
				}
			}
			if exactKeyObserved {
				continue
			}
			if len(observedItems) > 1 {
				// The ID appeared under multiple keys; keep the ledger evidence
				// rather than guessing which observation should own it.
				continue
			}
			observedRuntime := observedItems[0]
			if _, declared := known[observedRuntime.Key]; declared {
				updated := row
				updated.Key = observedRuntime.Key
				if observedRuntime.Name != "" {
					updated.Destination = observedRuntime.Name
				}
				updated.URL = observedRuntime.URL
				updated.LastAction = observedRuntime.LastAction
				updated.LastActionAt = observedRuntime.LastActionAt
				if err := s.Store.RekeyRuntimeRecord(row, updated); err != nil {
					return fmt.Errorf("rekey runtime metadata %q to observed MCP: %w", row.SourcePath, err)
				}
				continue
			}
			// A live ID observed under a key not declared for this capability
			// cannot be safely controlled from this row; discard only stale
			// manager metadata below.
		}
		if _, keyIsDeclared := known[row.Key]; keyIsDeclared && (!observationAvailable || len(observedItems) == 0) {
			// Preserve declared rows when the runtime is missing or could not be
			// observed. Absence is not proof that the ledger row is orphaned.
			continue
		}
		if err := s.Store.Remove(row); err != nil {
			return fmt.Errorf("remove orphan runtime metadata %q: %w", row.SourcePath, err)
		}
	}
	return nil
}
