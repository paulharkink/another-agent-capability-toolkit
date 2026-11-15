package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostAuthenticatesBeforeDispatchingAnyService(t *testing.T) {
	host := NewAuthenticatedHost(func(header string) (ClientIdentity, bool) {
		return ClientIdentity{Instance: "instance-a", ContainerID: "container-a"}, header == "Bearer valid"
	})
	for _, route := range []string{"/vault/", "/browser/"} {
		route := route
		if err := host.Register(route, route, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			client, ok := ClientFromContext(r.Context())
			if !ok || client.Instance != "instance-a" || client.ContainerID != "container-a" {
				t.Errorf("client = %#v, %v", client, ok)
			}
			w.WriteHeader(http.StatusNoContent)
		})); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			token  string
			status int
		}{{"", 401}, {"Bearer invalid", 401}, {"Bearer valid", 204}} {
			req := httptest.NewRequest(http.MethodGet, route, nil)
			req.Header.Set("Authorization", tc.token)
			resp := httptest.NewRecorder()
			host.ServeHTTP(resp, req)
			if resp.Code != tc.status {
				t.Errorf("%s %q: got %d, want %d", route, tc.token, resp.Code, tc.status)
			}
		}
	}
}

func TestAuthenticatedHostFailsClosedWithoutAuthenticator(t *testing.T) {
	host := NewAuthenticatedHost(nil)
	if err := host.Register("service", "/", httpHandler("unexpected")); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	host.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want unauthorized", response.Code)
	}
}
