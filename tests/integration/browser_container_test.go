package integration

import (
	"os"
	"strings"
	"testing"
)

func TestBrowserAuthenticationHelperImageContract(t *testing.T) {
	const root = "../../images/browser-authentication-helper"
	dockerfile, err := os.ReadFile(root + "/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfileText := string(dockerfile)
	for _, required := range []string{
		"LABEL org.aact.browser-authentication-helper=\"1\"",
		"COPY browser_auth.py /opt/browser-authentication-helper/browser_auth.py",
		"COPY --chmod=0755 browser-authentication-helper /usr/local/bin/browser-authentication-helper",
		"FROM kasmweb/chromium:1.18.0",
		"APP_ARGS=\"",
		"--start-maximized",
		"--user-data-dir=/home/kasm-user/.config/chromium",
		"--remote-debugging-port=9222",
		"--remote-debugging-address=127.0.0.1",
		"--load-extension=/opt/aact-browser-access/runtime",
	} {
		if !strings.Contains(dockerfileText, required) {
			t.Errorf("Dockerfile is missing %q", required)
		}
	}
	instructions, err := os.ReadFile(root + "/README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"aact/browser-authentication-helper:1.18.0",
		"--publish 127.0.0.1:6901:6901",
		"--shm-size=512m",
		"http://localhost:6901",
		"No Kasm username or password is required",
		"browser-authentication-helper run-adapter",
		"127.0.0.1:9222",
		"never the refresh token",
	} {
		if !strings.Contains(string(instructions), required) {
			t.Errorf("run instructions are missing %q", required)
		}
	}
	if strings.Contains(string(instructions), "VNC_PW") {
		t.Error("passwordless run instructions must not require VNC_PW")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}
