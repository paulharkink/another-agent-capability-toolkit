package vault

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulharkink/another-agent-capability-toolkit/internal/daemon"
)

func TestDaemonHostUsesExistingScopedCredentialForMultipleServices(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "credentials.kdbx"), []byte("synthetic-password"))
	if err != nil {
		t.Fatal(err)
	}
	namespace := strings.Repeat("a", 64)
	api := NewAPI(store, nil)
	token, err := api.IssuePrivateInputToken(namespace, []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := api.BindTokenContainer(token, "container-a"); err != nil {
		t.Fatal(err)
	}
	host := daemon.NewAuthenticatedHost(api.AuthenticateDaemonRequest)
	if err := host.Register("vault", "/", api); err != nil {
		t.Fatal(err)
	}
	if err := host.Register("browser", "/browser/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, ok := daemon.ClientFromContext(r.Context())
		if !ok || client.Instance != namespace || client.ContainerID != "container-a" {
			t.Errorf("incorrect authenticated instance")
		}
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatal(err)
	}
	request := func(path, bearer string) int {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		host.ServeHTTP(w, r)
		return w.Code
	}
	if got := request("/browser/", token); got != http.StatusNoContent {
		t.Fatalf("browser status %d", got)
	}
	if got := request("/v1/secrets", token); got != http.StatusForbidden {
		t.Fatalf("vault scope status %d", got)
	}
	if err := api.RevokeToken(token); err != nil {
		t.Fatal(err)
	}
	if got := request("/browser/", token); got != http.StatusUnauthorized {
		t.Fatalf("revoked status %d", got)
	}
}
