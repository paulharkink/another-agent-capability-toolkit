package state

import "testing"

const (
	testFileAllAccess uint32 = 0x001f01ff
	testGenericAll    uint32 = 0x10000000
)

func TestAuthDirACLPolicy(t *testing.T) {
	valid := authDirACLPolicy{
		OwnerIsCurrentUser: true,
		DACLPresent:        true,
		DACLProtected:      true,
		ACEs: []authDirACEPolicy{
			{Type: 0, Flags: 0, Mask: testFileAllAccess, SIDKnown: true, SIDIsCurrentUser: true},
			{Type: 0, Flags: 0x0b, Mask: testGenericAll, SIDKnown: true, SIDIsCurrentUser: true},
		},
	}

	tests := []struct {
		name  string
		state authDirACLPolicy
		want  bool
	}{
		{name: "current user owner", state: valid, want: true},
		{name: "elevated administrators owner", state: withAdminOwner(valid), want: true},
		{name: "ACE order does not matter", state: mutatePolicy(valid, func(p *authDirACLPolicy) { p.ACEs[0], p.ACEs[1] = p.ACEs[1], p.ACEs[0] }), want: true},
		{name: "unprotected DACL", state: mutatePolicy(valid, func(p *authDirACLPolicy) { p.DACLProtected = false })},
		{name: "unrecognized owner", state: mutatePolicy(valid, func(p *authDirACLPolicy) { p.OwnerIsCurrentUser = false })},
		{name: "missing DACL", state: mutatePolicy(valid, func(p *authDirACLPolicy) { p.DACLPresent = false })},
		{name: "missing ACE", state: mutatePolicy(valid, func(p *authDirACLPolicy) { p.ACEs = p.ACEs[:1] })},
		{name: "extra ACE", state: mutatePolicy(valid, func(p *authDirACLPolicy) { p.ACEs = append(p.ACEs, p.ACEs[0]) })},
		{name: "deny ACE", state: mutateACE(valid, 0, func(a *authDirACEPolicy) { a.Type = 1 })},
		{name: "wrong direct mask", state: mutateACE(valid, 0, func(a *authDirACEPolicy) { a.Mask = testGenericAll })},
		{name: "wrong child mask", state: mutateACE(valid, 1, func(a *authDirACEPolicy) { a.Mask = testFileAllAccess })},
		{name: "wrong direct flags", state: mutateACE(valid, 0, func(a *authDirACEPolicy) { a.Flags = 0x0b })},
		{name: "wrong child flags", state: mutateACE(valid, 1, func(a *authDirACEPolicy) { a.Flags = 0x03 })},
		{name: "other principal", state: mutateACE(valid, 1, func(a *authDirACEPolicy) { a.SIDIsCurrentUser = false })},
		{name: "unknown principal", state: mutateACE(valid, 1, func(a *authDirACEPolicy) { a.SIDKnown = false })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := authDirACLIsPrivate(tt.state); got != tt.want {
				t.Fatalf("authDirACLIsPrivate() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestAuthDirSIDLengthFitsACE(t *testing.T) {
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
			if got := authDirSIDLengthFitsACE(tt.aceSize, sidOffset, tt.count); got != tt.wantValid {
				t.Fatalf("authDirSIDLengthFitsACE(%d, %d, %d) = %t, want %t", tt.aceSize, sidOffset, tt.count, got, tt.wantValid)
			}
		})
	}
}

func withAdminOwner(policy authDirACLPolicy) authDirACLPolicy {
	policy.OwnerIsCurrentUser = false
	policy.OwnerIsAdministrators = true
	return policy
}

func mutatePolicy(policy authDirACLPolicy, mutate func(*authDirACLPolicy)) authDirACLPolicy {
	policy.ACEs = append([]authDirACEPolicy(nil), policy.ACEs...)
	mutate(&policy)
	return policy
}

func mutateACE(policy authDirACLPolicy, index int, mutate func(*authDirACEPolicy)) authDirACLPolicy {
	policy = mutatePolicy(policy, func(*authDirACLPolicy) {})
	mutate(&policy.ACEs[index])
	return policy
}
