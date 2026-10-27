package app

import (
	"context"
	"fmt"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

// reconcileRuntimeLedger repairs identities for declared MCPs and preserves
// undeclared runtime rows for explicit recovery from the instance UI. It never
// mutates Docker or agent registrations.
// Rows for declared MCPs are retained even if their runtime is missing. When
// Docker observes the same ID under a different declared key, the ledger row
// is rekeyed to keep the visible runtime manageable. Undeclared rows remain
// available for explicit recovery; this function never mutates Docker or
// agent registrations.
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
			// List includes synthetic missing rows for ledger entries that were
			// absent from the fresh Docker inventory. They are not evidence that
			// the container is still present.
			if item.ID == "" || item.Status == "missing" {
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
			// Keep an ID observed under an undeclared key linked to its ledger
			// identity. The instance UI exposes it for exact-ID recovery; it must
			// not disappear from view merely because its definition was removed.
		}
		if _, keyIsDeclared := known[row.Key]; !keyIsDeclared {
			// Keep stale undeclared records visible so the user can explicitly
			// forget them or remove the exact locally owned runtime.
			continue
		}
		if observationAvailable && len(observedItems) == 0 && row.AgentID == "docker" && row.LastAction == "stop" {
			// A successful stop removes the runtime. Once a fresh Docker list
			// confirms that exact ID is absent, discard only its stale record so
			// it cannot conflict with a later replacement runtime.
			if err := s.Store.Remove(row); err != nil {
				return fmt.Errorf("remove stopped runtime metadata %q: %w", row.SourcePath, err)
			}
			continue
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
