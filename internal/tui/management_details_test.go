package tui

import (
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

func TestAgentManagementDetailsShowsEvidenceAndEveryPathWithoutMasking(t *testing.T) {
	row := viewmodel.AgentManagementRow{
		ID: "codex", Name: "Codex", Detection: "installed",
		Evidence: "CLI executable: /usr/bin/codex", Home: "/home/alice/.codex-home",
		EffectiveConfigPath: "/home/alice/.codex/config.toml",
		WriteConfigPath:     "/home/alice/.codex-home/config.toml",
		Note:                "Process home override differs from profile home",
		ConfigFiles: []viewmodel.AgentConfigFile{
			{Path: "/home/alice/.codex/config.toml", Scope: "user", Precedence: "native", Evidence: "process-native", Exists: true},
			{Path: "/home/alice/.codex-home/config.toml", Scope: "profile", Precedence: "recorded", Profile: "work", Home: "/home/alice/.codex-home", Exists: false},
		},
		Registrations: []string{"team / inspect / dev / production"},
	}
	got := strings.Join(agentManagementDetails(row), "\n")
	for _, want := range []string{
		"Status: Detected", "CLI executable: /usr/bin/codex",
		"Active home: /home/alice/.codex-home", "Effective config: /home/alice/.codex/config.toml",
		"Intended write config: /home/alice/.codex-home/config.toml", "process home override",
		"/home/alice/.codex/config.toml", "/home/alice/.codex-home/config.toml", "work", "team / inspect / dev / production",
	} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Errorf("details missing %q:\n%s", want, got)
		}
	}
}

func TestAgentManagementStatusKeepsDetectionSeparateFromConfigPresence(t *testing.T) {
	cases := []struct{ detection, want string }{
		{"installed", "Detected"},
		{"not-detected", "Not detected"},
		{"unverified", "Unverified"},
		{"unsupported", "Unsupported here"},
		{"failed: permission denied", "Unverified"},
	}
	for _, tc := range cases {
		if got := agentManagementStatus(tc.detection); got != tc.want {
			t.Errorf("agentManagementStatus(%q) = %q, want %q", tc.detection, got, tc.want)
		}
	}
	row := viewmodel.AgentManagementRow{Detection: "not-detected", ConfigFiles: []viewmodel.AgentConfigFile{{Path: "/tmp/existing.json", Exists: true}}}
	if got := agentManagementStatus(row.Detection); got != "Not detected" {
		t.Fatalf("existing config changed client detection: %q", got)
	}
}
