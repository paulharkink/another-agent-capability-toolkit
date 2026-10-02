package config

import (
	"encoding/json"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/catalog"
	"testing"
)

func TestTargetJSONProtocol(t *testing.T) {
	data, e := json.Marshal(Target{Environment: "dev", Name: "production", Path: "/tmp/production.toml", Raw: map[string]any{"cluster": map[string]any{"server": "https://cluster.example"}}})
	if e != nil {
		t.Fatal(e)
	}
	var raw map[string]any
	if e = json.Unmarshal(data, &raw); e != nil {
		t.Fatal(e)
	}
	if raw["environment"] != "dev" || raw["name"] != "production" || raw["path"] != "/tmp/production.toml" || raw["raw"] == nil || raw["Raw"] != nil {
		t.Fatalf("%s", data)
	}
}
func TestSourceJSONCatalog(t *testing.T) {
	data, e := json.Marshal(Source{ID: "source", PackageDefaults: map[string]map[string]any{"plain": {"team": "Platform"}}, Catalog: []catalog.Package{{SchemaVersion: 1, ID: "plain", Skill: &catalog.Skill{Name: "plain"}}}})
	if e != nil {
		t.Fatal(e)
	}
	var raw map[string]any
	if e = json.Unmarshal(data, &raw); e != nil {
		t.Fatal(e)
	}
	if raw["id"] != "source" || raw["package_defaults"] == nil {
		t.Fatalf("%s", data)
	}
	entry := raw["catalog"].([]any)[0].(map[string]any)
	if entry["schema_version"] != float64(1) || entry["skill"].(map[string]any)["name"] != "plain" {
		t.Fatalf("%s", data)
	}
}
