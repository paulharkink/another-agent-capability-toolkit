package vault

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerHandoffArrivesOnStdinAndMaterializesPrivateTmpfsFile(t *testing.T) {
	info := DaemonInfo{APIAddress: "https://host.docker.internal:12345", APICertificate: "public-cert"}
	handoff := ContainerHandoff{APIAddress: info.APIAddress, Certificate: info.APICertificate, Token: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
	var stdin strings.Builder
	if err := WriteContainerHandoff(&stdin, handoff); err != nil {
		t.Fatal(err)
	}
	var decoded ContainerHandoff
	if err := json.Unmarshal([]byte(stdin.String()), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Token != handoff.Token || decoded.APIAddress != handoff.APIAddress {
		t.Fatalf("stdin handoff=%#v", decoded)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "vault.json")
	if err := ReceiveContainerHandoff(strings.NewReader(stdin.String()), path); err != nil {
		t.Fatal(err)
	}
	infoOnDisk, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := infoOnDisk.Mode().Perm(); got != 0400 {
		t.Fatalf("handoff file mode=%04o, want 0400", got)
	}
	if !strings.Contains(strings.TrimSpace(stdin.String()), handoff.Token) {
		t.Fatal("test setup failed to include stdin token")
	}
}

func TestContainerHandoffRejectsLoopbackOnlyDaemon(t *testing.T) {
	info := DaemonInfo{APIAddress: "https://localhost:12345", APICertificate: "public-cert"}
	if _, err := NewContainerHandoff(info, base64.RawURLEncoding.EncodeToString(make([]byte, 32))); err == nil {
		t.Fatal("container handoff accepted a loopback-only API listener")
	}
}
