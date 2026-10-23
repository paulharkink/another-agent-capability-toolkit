package app

import (
	"fmt"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/config"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/mcp"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/state"
)

// reconcileRuntimeLedger removes only local manager metadata for runtime rows
// that cannot be linked to a declared MCP under any visible instance of this
// capability. It never queries or mutates Docker or agent registrations.
// Rows for declared MCPs are retained even if their runtime is missing. When
// Docker observes the same ID under a different key, only the stale ledger
// metadata is removed; the actual runtime remains visible under its observed
// key and is never changed here.
func (s *Service) reconcileRuntimeLedger(p catalog.Package, instances []config.Instance, observed []mcp.Instance, observationAvailable bool) error {
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
	observedKeysByID := make(map[string]map[state.Key]struct{})
	if observationAvailable {
		for _, item := range observed {
			if item.ID == "" {
				continue
			}
			if observedKeysByID[item.ID] == nil {
				observedKeysByID[item.ID] = make(map[state.Key]struct{})
			}
			observedKeysByID[item.ID][item.Key] = struct{}{}
		}
	}
	for _, row := range rows {
		if row.Component != "runtime" || row.Key.Source != s.Source.ID || row.Key.Package != p.ID {
			continue
		}
		_, keyIsDeclared := known[row.Key]
		if keyIsDeclared {
			observedKeys := observedKeysByID[row.SourcePath]
			if !observationAvailable || len(observedKeys) == 0 {
				continue
			}
			if _, exactKeyObserved := observedKeys[row.Key]; exactKeyObserved {
				continue
			}
		}
		if err := s.Store.Remove(row); err != nil {
			return fmt.Errorf("remove orphan runtime metadata %q: %w", row.SourcePath, err)
		}
	}
	return nil
}
