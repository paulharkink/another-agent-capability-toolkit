package state

import "testing"

func TestAuthDirDiagnosticSIDFitsACE(t *testing.T) {
	const sidOffset = uintptr(8)
	tests := []struct {
		name      string
		aceSize   uint16
		count     uint8
		wantValid bool
	}{
		{name: "complete SID without subauthorities", aceSize: 16, count: 0, wantValid: true},
		{name: "complete SID with subauthorities", aceSize: 28, count: 3, wantValid: true},
		{name: "truncated SID body", aceSize: 27, count: 3},
		{name: "truncated SID header", aceSize: 15, count: 0},
		{name: "SID offset beyond ACE", aceSize: 8, count: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := authDirDiagnosticSIDFitsACE(tt.aceSize, sidOffset, tt.count); got != tt.wantValid {
				t.Fatalf("authDirDiagnosticSIDFitsACE(%d, %d, %d) = %t, want %t", tt.aceSize, sidOffset, tt.count, got, tt.wantValid)
			}
		})
	}
}
