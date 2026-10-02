package main

import "testing"

// Changing URL parsing, lowercasing coordinates, or dropping ports loses identity.
func TestHTTPSAndSSHRemoteNormalization(t *testing.T) {
	tests := []struct{ remote, host, name string }{
		{"https://Git.Example/Team/Widget.git", "git.example", "Team/Widget"},
		{"git@Git.Example:Team/Widget.git", "git.example", "Team/Widget"},
		{"ssh://git@git.example/Team/Widget.git", "git.example", "Team/Widget"},
		{"ssh://git@git.example:2222/Team/Widget.git", "git.example:2222", "Team/Widget"},
		{"https://user:secret@git.example:8443/Team/Widget.git", "git.example:8443", "Team/Widget"},
		{"https://git.example:443/team/widget.git/", "git.example", "team/widget"},
		{"ssh://git@git.example:22/team/widget.git", "git.example", "team/widget"},
		{"ssh://git@[2001:db8::1]:2222/team/widget.git", "[2001:db8::1]:2222", "team/widget"},
		{"https://git.example/team/a%20b.git", "git.example", "team/a b"},
	}
	for _, tc := range tests {
		t.Run(tc.remote, func(t *testing.T) {
			host, name, err := NormalizeRemote(tc.remote)
			if err != nil || host != tc.host || name != tc.name {
				t.Fatalf("got (%q,%q,%v), want (%q,%q)", host, name, err, tc.host, tc.name)
			}
		})
	}
}

func TestRejectNonNetworkRemotes(t *testing.T) {
	for _, remote := range []string{"", "../local", "/tmp/repo", "C:\\repos\\thing", "file:///tmp/repo", "https://git.example", "https://git.example/a?token=secret", "https://git.example/a#fragment", "https://git.example/../a"} {
		if _, _, err := NormalizeRemote(remote); err == nil {
			t.Errorf("accepted %q", remote)
		}
	}
}
