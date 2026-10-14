package viewmodel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuthenticationFailedJSONRemainsStringStatus(t *testing.T) {
	encoded, err := json.Marshal(OperationResult{AuthenticationStatus: AuthenticationFailed})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if got := fields["AuthenticationStatus"]; got != AuthenticationFailed {
		t.Fatalf("serialized authentication status = %#v, want %q; JSON: %s", got, AuthenticationFailed, encoded)
	}
	if strings.Contains(strings.ToLower(string(encoded)), "authenticated") {
		t.Fatalf("operation JSON added an authentication boolean alongside its status: %s", encoded)
	}
}
