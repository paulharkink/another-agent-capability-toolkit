package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func httpHandler(value string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(value))
	})
}

func TestRunDispatchesConfiguredServices(t *testing.T) {
	configuration := json.RawMessage(`{"setting":"value"}`)
	input, err := json.Marshal(Bootstrap{Services: []ServiceConfig{{Name: "example", Configuration: configuration}}})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = Run(context.Background(), strings.NewReader(string(input)), Registry{
		"example": func(_ context.Context, got json.RawMessage) error {
			called = true
			if string(got) != string(configuration) {
				t.Fatalf("configuration = %s, want %s", got, configuration)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("configured service was not run")
	}
}

func TestRunRejectsUnknownService(t *testing.T) {
	input, err := json.Marshal(Bootstrap{Services: []ServiceConfig{{Name: "missing", Configuration: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	err = Run(context.Background(), strings.NewReader(string(input)), Registry{})
	if err == nil || !strings.Contains(err.Error(), `unknown daemon service "missing"`) {
		t.Fatalf("Run error = %v, want unknown service", err)
	}
}

func TestHostRoutesNamedServices(t *testing.T) {
	host := NewHost()
	if err := host.Register("vault", "/", httpHandler("vault")); err != nil {
		t.Fatal(err)
	}
	if err := host.Register("future", "/future/", httpHandler("future")); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"/v1/secrets": "vault", "/future/status": "future"} {
		response := httptest.NewRecorder()
		host.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Body.String() != want {
			t.Errorf("GET %s body = %q, want %q", path, response.Body.String(), want)
		}
	}
}

func TestHostRejectsDuplicateServiceName(t *testing.T) {
	host := NewHost()
	if err := host.Register("vault", "/", httpHandler("vault")); err != nil {
		t.Fatal(err)
	}
	if err := host.Register("vault", "/again/", httpHandler("again")); err == nil {
		t.Fatal("duplicate service name was accepted")
	}
}
