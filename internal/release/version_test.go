package release

import (
	"strings"
	"testing"
)

func TestNextVersion(t *testing.T) {
	tests := []struct {
		name    string
		current string
		bump    string
		want    string
	}{
		{name: "patch", current: "v0.1.3", bump: "patch", want: "v0.1.4"},
		{name: "minor resets patch", current: "v0.1.3", bump: "minor", want: "v0.2.0"},
		{name: "major resets minor and patch", current: "v0.1.3", bump: "major", want: "v1.0.0"},
		{name: "reject unsupported bump", current: "v0.1.3", bump: "build", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextVersion(tt.current, tt.bump)
			if tt.want == "" {
				if err == nil {
					t.Fatal("expected invalid bump to fail")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("NextVersion(%q, %q) = %q, want %q", tt.current, tt.bump, got, tt.want)
			}
		})
	}
}

func TestUpdateReadmeInstallVersion(t *testing.T) {
	readme := "# AACT\n\n## Install\n\n```sh\ncurl -fsSL https://raw.githubusercontent.com/paulharkink/another-agent-capability-toolkit/main/install.sh | sh -s -- -v 0.1.3\n```\n"
	got, err := UpdateReadmeInstallVersion(readme, "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	want := "curl -fsSL https://raw.githubusercontent.com/paulharkink/another-agent-capability-toolkit/main/install.sh | sh -s -- -v 0.2.0"
	if !containsLine(got, want) {
		t.Fatalf("updated README does not contain install line %q:\n%s", want, got)
	}
	if containsLine(got, "curl -fsSL https://raw.githubusercontent.com/paulharkink/another-agent-capability-toolkit/main/install.sh | sh -s -- -v 0.1.3") {
		t.Fatalf("updated README still contains the previous install version:\n%s", got)
	}
}

func TestReadmeInstallVersionRequiresExactlyOneCommand(t *testing.T) {
	command := "curl -fsSL https://raw.githubusercontent.com/paulharkink/another-agent-capability-toolkit/main/install.sh | sh -s -- -v 0.1.3"
	for _, tt := range []struct {
		name    string
		readme  string
		wantErr bool
	}{
		{name: "one command", readme: "```sh\n" + command + "\n```"},
		{name: "missing command", readme: "## Install\n", wantErr: true},
		{name: "duplicate commands", readme: command + "\n" + command, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadmeInstallVersion(tt.readme)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReadmeInstallVersion() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func containsLine(content, want string) bool {
	for _, line := range strings.Split(content, "\n") {
		if line == want {
			return true
		}
	}
	return false
}
