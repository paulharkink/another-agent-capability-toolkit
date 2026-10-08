package catalog

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInstallationItemsLinkDeclaredComponents(t *testing.T) {
	p := Package{Skills: []Skill{{Name: "review-guidance"}, {Name: "release-notes"}}, MCPs: []MCP{{Name: "repository-api"}}}
	items, err := InstallationItems(p, []ComponentSet{{Name: "repository-access", Skills: []string{"review-guidance"}, MCPs: []string{"repository-api"}}})
	if err != nil || len(items) != 2 {
		t.Fatalf("items: %#v, %v", items, err)
	}
	if !reflect.DeepEqual(items[0].Skills, []string{"review-guidance"}) || !reflect.DeepEqual(items[0].MCPs, []string{"repository-api"}) || !reflect.DeepEqual(items[1].Skills, []string{"release-notes"}) {
		t.Fatalf("incorrect linked items: %#v", items)
	}
	for _, sets := range [][]ComponentSet{
		{{Name: "unknown", Skills: []string{"missing"}}},
		{{Name: "first", Skills: []string{"review-guidance"}}, {Name: "second", Skills: []string{"review-guidance"}}},
	} {
		if _, err := InstallationItems(p, sets); err == nil {
			t.Fatalf("invalid relationship accepted: %#v", sets)
		}
	}
}

func TestInstallationItemsLegacyAndSameNamedKinds(t *testing.T) {
	p := Package{Dir: "/definition", Skill: &Skill{Name: "guidance"}, Templates: []Template{{Source: "SKILL.md.mustache", Destination: "SKILL.md"}}, Generator: &Command{Argv: []string{"./generate"}}, MCP: &MCP{Name: "service", EnabledInput: "enabled"}}
	items, err := InstallationItems(p, nil)
	if err != nil || len(items) != 1 || len(items[0].Skills) != 1 || len(items[0].MCPs) != 1 || p.MCP.EnabledInput != "enabled" {
		t.Fatalf("legacy linked item: %#v, %v", items, err)
	}
	skills := p.SkillDefinitions()
	if len(skills) != 1 || skills[0].Source != "/definition" || len(skills[0].Templates) != 1 || skills[0].Generator == nil {
		t.Fatalf("legacy resources lost: %#v", skills)
	}
	p = Package{Skills: []Skill{{Name: "same"}}, MCPs: []MCP{{Name: "same"}}, Plugins: []Plugin{{Name: "same", Format: "native", Source: "./plugin"}}}
	items, err = InstallationItems(p, nil)
	if err != nil || len(items) != 3 || items[0].ID == items[1].ID || items[1].ID == items[2].ID {
		t.Fatalf("component kinds collide: %#v, %v", items, err)
	}
}

// Rejecting a declared skill list would prevent a whole capability's skills
// from being available to the installer.
func TestLoadMultipleSkillsWithIndependentRoots(t *testing.T) {
	dir := writeManifest(t, `schema_version = 1
id = "guidance"
name = "Guidance"
[[skills]]
name = "review-guidance"
source = "./skills/review-guidance"
[[skills.templates]]
source = "SKILL.md.mustache"
destination = "SKILL.md"
[[skills]]
name = "release-notes"
source = "./skills/release-notes"
[skills.generator]
command = ["./generate.sh"]
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Skills []struct {
			Name, Source string
			Templates    []Template
			Generator    *Command
		}
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != 2 {
		t.Fatalf("want two skills, got %s", data)
	}
	if got.Skills[0].Name != "review-guidance" || got.Skills[0].Source != filepath.Join(dir, "skills", "review-guidance") || len(got.Skills[0].Templates) != 1 {
		t.Fatalf("first skill root/templates lost: %#v", got.Skills[0])
	}
	if got.Skills[1].Source != filepath.Join(dir, "skills", "release-notes") || got.Skills[1].Generator == nil || got.Skills[1].Generator.TimeoutSeconds != 300 {
		t.Fatalf("second skill root/generator lost: %#v", got.Skills[1])
	}
}

func TestLoadPluginOnlyCapability(t *testing.T) {
	_, err := Load(writeManifest(t, `schema_version = 1
id = "plugin-kit"
name = "Plugin Kit"
[[plugins]]
name = "guidance"
format = "claude-code"
source = "./native-plugin"
`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsInvalidComponentDeclarations(t *testing.T) {
	for _, tc := range []struct{ name, manifest, want string }{
		{"mixed", plainManifest + "[[skills]]\nname = \"other\"\nsource = \"./other\"\n", "mix"},
		{"duplicate", "schema_version = 1\nid = \"kit\"\nname = \"Kit\"\n[[skills]]\nname = \"one\"\n[[skills]]\nname = \"one\"\n", "duplicate skill"},
		{"plugin-source", "schema_version = 1\nid = \"kit\"\nname = \"Kit\"\n[[plugins]]\nname = \"native\"\nformat = \"claude-code\"\n", "source"},
		{"generator-timeout", "schema_version = 1\nid = \"kit\"\nname = \"Kit\"\n[[skills]]\nname = \"one\"\n[skills.generator]\ncommand = [\"./generate\"]\ntimeout_seconds = 0\n", "timeout"},
		{"plugin-enable", "schema_version = 1\nid = \"kit\"\nname = \"Kit\"\n[[plugins]]\nname = \"native\"\nformat = \"claude-code\"\nsource = \"./native\"\nenabled_input = \"missing\"\n", "enabled_input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeManifest(t, tc.manifest))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %s error, got %v", tc.want, err)
			}
		})
	}
}
