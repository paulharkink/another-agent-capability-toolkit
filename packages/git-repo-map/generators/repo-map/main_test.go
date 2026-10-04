package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProtocolHelperProcess(t *testing.T) {
	if os.Getenv("AACT_REPO_MAP_HELPER") != "1" {
		return
	}
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}
func helper(t *testing.T, body string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProtocolHelperProcess$")
	// The released program's runtime does not require Git, Go, find or a shell.
	cmd.Env = append(os.Environ(), "AACT_REPO_MAP_HELPER=1", "PATH=")
	cmd.Stdin = strings.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}
func TestProtocolSubprocess(t *testing.T) {
	root := t.TempDir()
	p := fixtureRepo(t, filepath.Join(root, "checkout"), "git@git.example:team/app.git")
	body, _ := json.Marshal(map[string]any{"protocol_version": 1, "inputs": map[string]any{"scan_roots": []string{root}, "hosts": []string{"git.example"}}, "context": map[string]any{"target": "fixture"}})
	stdout, stderr, err := helper(t, string(body))
	if err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	var result struct {
		Repositories []Repository `json:"repositories"`
	}
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	if !reflect.DeepEqual(result.Repositories, []Repository{{"git.example", "team/app", p}}) {
		t.Fatalf("unexpected repositories %#v", result.Repositories)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout has extra output: %q", stdout)
	}
	if !strings.Contains(stderr, "checkout") {
		t.Fatalf("missing progress: %q", stderr)
	}
}
func TestProtocolRejectsInvalidInput(t *testing.T) {
	for _, body := range []string{
		`{"protocol_version":2,"inputs":{"scan_roots":["."]}}`,
		`{"protocol_version":1,"inputs":{"scan_roots":[]}}`,
		`{"protocol_version":1,"inputs":{"scan_roots":"."}}`,
		`{"protocol_version":1,"inputs":{"scan_roots":[""]}}`,
		`{"protocol_version":1,"inputs":{"scan_roots":["."],"hosts":"git.example"}}`,
		`{"protocol_version":1,"inputs":{"scan_roots":["."]}} {}`,
		`{"protocol_version":1`,
		`null`,
	} {
		t.Run(body, func(t *testing.T) {
			stdout, stderr, err := helper(t, body)
			if err == nil || stdout != "" || stderr == "" {
				t.Fatalf("got stdout=%q stderr=%q err=%v", stdout, stderr, err)
			}
		})
	}
}
func TestProtocolEmptyResultIsArray(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"protocol_version": 1, "inputs": map[string]any{"scan_roots": []string{t.TempDir()}}})
	stdout, stderr, err := helper(t, string(body))
	if err != nil {
		t.Fatalf("%v %s", err, stderr)
	}
	if strings.TrimSpace(stdout) != `{"repositories":[]}` {
		t.Fatalf("got %q", stdout)
	}
}

func TestProtocolSilentlyIgnoresInvalidNestedCheckout(t *testing.T) {
	root := t.TempDir()
	fixtureRepo(t, filepath.Join(root, "valid"), "https://git.example/team/app.git")
	invalid := filepath.Join(root, "stale")
	if err := os.Mkdir(invalid, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalid, ".git"), []byte("invalid git file\n"), 0644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"protocol_version": 1, "inputs": map[string]any{"scan_roots": []string{root}}})
	stdout, stderr, err := helper(t, string(body))
	if err != nil || !strings.Contains(stdout, `"team/app"`) || strings.Contains(stderr, invalid) {
		t.Fatalf("stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
}
func TestProtocolRootFailureHasNoPartialOutput(t *testing.T) {
	root := t.TempDir()
	fixtureRepo(t, filepath.Join(root, "ok"), "https://git.example/team/app.git")
	missing := filepath.Join(root, "missing")
	body, _ := json.Marshal(map[string]any{"protocol_version": 1, "inputs": map[string]any{"scan_roots": []string{root, missing}}})
	stdout, stderr, err := helper(t, string(body))
	if err == nil || stdout != "" || !strings.Contains(stderr, missing) {
		t.Fatalf("got %q %q %v", stdout, stderr, err)
	}
}
