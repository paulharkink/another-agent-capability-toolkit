package browsercontroller

import "testing"

func TestBrowserPolicySupportsBlocklistAndAutomaticPopups(t *testing.T) {
	policy := `{"allowed_urls":["https://example.test"],"blocked_urls":["https://blocked.example.test"],"auto_allow_popups":true}`
	if err := ValidateAccessConfig(policy); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStartupURLAgainstAccessConfig("https://blocked.example.test", policy); err == nil {
		t.Fatal("blocked URL allowed")
	}
	if err := ValidateStartupURLAgainstAccessConfig("https://example.test", policy); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAccessConfig(`{"auto_allow_popups":true}`); err == nil {
		t.Fatal("automatic popups without allowed URLs accepted")
	}
}
