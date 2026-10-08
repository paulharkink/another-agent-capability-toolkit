package app

import (
	"strings"
	"testing"
)

func TestAuthenticationRequiredMessagePutsProviderDiagnosticBeforeRetryGuidance(t *testing.T) {
	message := authenticationRequiredMessage("demo", "  Kubernetes API rejected credentials (HTTP 401).  ")
	if !strings.HasPrefix(message, "Kubernetes API rejected credentials (HTTP 401).") {
		t.Fatalf("provider diagnostic is not the primary message: %q", message)
	}
	if !strings.Contains(message, "aact mcp authenticate demo") || !strings.Contains(message, "--interactive") {
		t.Fatalf("retry guidance missing: %q", message)
	}
}

func TestAuthenticationRequiredMessagePreservesLegacyFallbackWhenDiagnosticIsEmpty(t *testing.T) {
	const want = "demo authentication required; run aact mcp authenticate demo with credentials or --interactive"
	if got := authenticationRequiredMessage("demo", " \n "); got != want {
		t.Fatalf("legacy fallback = %q, want %q", got, want)
	}
}
