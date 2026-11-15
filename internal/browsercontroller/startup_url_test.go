package browsercontroller

import "testing"

func TestValidateStartupURLAllowsSafeHTTPOriginsAndPaths(t *testing.T) {
	for _, value := range []string{"https://app.example", "https://service123.example/login/sso-1", "http://127.0.0.1:8080/start"} {
		if err := ValidateStartupURL(value); err != nil {
			t.Errorf("ValidateStartupURL(%q) error = %v", value, err)
		}
	}
}

func TestValidateStartupURLRejectsCredentialsSecretsAndShellSyntax(t *testing.T) {
	for _, value := range []string{
		"javascript:alert(1)", "https://user:pass@example.test", "https://example.test/?access_token=secret",
		"https://example.test/#secret", "https://example.test/\r\nKASM_URL=evil", "https://example.test/a;$HOME",
		"https://example.test/a*", "https://example.test:99999/", "https://exämple.test/", "https://example.test/../admin", "https://[::1]/",
		"https://example.test//a", "https://example.test/%2e%2e/admin", "https://123456789/", "https://Example.test/", "https://example.test:0443/",
		"https://0x7f.1/", "https://127.1/", "https://0177.0.0.1/", "https://0x7f000001/", "https://example.123/",
	} {
		if err := ValidateStartupURL(value); err == nil {
			t.Errorf("ValidateStartupURL(%q) accepted unsafe URL", value)
		}
	}
}

func TestValidateStartupURLAgainstBrowserAllowlist(t *testing.T) {
	for _, test := range []struct {
		name    string
		startup string
		config  string
		wantErr bool
	}{
		{name: "unrestricted", startup: "https://app.example", config: "{}"},
		{name: "domain host includes subdomain", startup: "https://app.example/login", config: `{"allowed_urls":["https://example"]}`},
		{name: "matching path prefix", startup: "https://app.example/login", config: `{"allowed_urls":["https://app.example/login"]}`},
		{name: "root path prefix matches URL origin", startup: "https://app.example", config: `{"allowed_urls":["https://app.example/"]}`},
		{name: "blocked host", startup: "https://app.example", config: `{"allowed_urls":["https://other.example"]}`, wantErr: true},
		{name: "blocked path", startup: "https://app.example/login", config: `{"allowed_urls":["https://app.example/app"]}`, wantErr: true},
		{name: "port mismatch", startup: "https://app.example:8443/login", config: `{"allowed_urls":["https://app.example:443"]}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateStartupURLAgainstAccessConfig(test.startup, test.config)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateStartupURLAgainstAccessConfig() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
