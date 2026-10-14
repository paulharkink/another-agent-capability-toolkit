package state

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKeyReadsLegacyProfileAsMCPDefinitionAndWritesCanonicalName(t *testing.T) {
	var legacy Key
	if err := json.Unmarshal([]byte(`{"source":"team","package":"repo","target":"work","profile":"github"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.MCPDefinition != "github" {
		t.Fatalf("legacy profile selector = %q, want MCP definition github", legacy.MCPDefinition)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"mcp_definition":"github"`) || strings.Contains(string(encoded), `"profile"`) {
		t.Fatalf("key did not write canonical selector field: %s", encoded)
	}
	var roundTrip Key
	if err := json.Unmarshal(encoded, &roundTrip); err != nil || roundTrip != legacy {
		t.Fatalf("canonical round trip = %#v, err %v", roundTrip, err)
	}
}
func TestKeyExplicitCanonicalMCPDefinitionBeatsLegacyProfile(t *testing.T) {
	var key Key
	if err := json.Unmarshal([]byte(`{"mcp_definition":"","profile":"github"}`), &key); err != nil {
		t.Fatal(err)
	}
	if key.MCPDefinition != "" {
		t.Fatalf("explicit canonical empty was replaced by legacy value %q", key.MCPDefinition)
	}
}
