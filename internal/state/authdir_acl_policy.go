package state

const (
	authDirAllowedACEType uint8  = 0
	authDirFileAllAccess  uint32 = 0x001f01ff
	authDirGenericAll     uint32 = 0x10000000
	authDirChildACEFlags  uint8  = 0x0b // OBJECT_INHERIT | CONTAINER_INHERIT | INHERIT_ONLY
)

type authDirACEPolicy struct {
	Type             uint8
	Flags            uint8
	Mask             uint32
	SIDKnown         bool
	SIDIsCurrentUser bool
}

type authDirACLPolicy struct {
	OwnerIsCurrentUser    bool
	OwnerIsAdministrators bool
	DACLPresent           bool
	DACLProtected         bool
	ACEs                  []authDirACEPolicy
}

func authDirACLIsPrivate(policy authDirACLPolicy) bool {
	if (!policy.OwnerIsCurrentUser && !policy.OwnerIsAdministrators) || !policy.DACLPresent || !policy.DACLProtected || len(policy.ACEs) != 2 {
		return false
	}

	directACE, childACE := false, false
	for _, ace := range policy.ACEs {
		if ace.Type != authDirAllowedACEType || !ace.SIDKnown || !ace.SIDIsCurrentUser {
			return false
		}
		switch {
		case ace.Flags == 0 && ace.Mask == authDirFileAllAccess && !directACE:
			directACE = true
		case ace.Flags == authDirChildACEFlags && ace.Mask == authDirGenericAll && !childACE:
			childACE = true
		default:
			return false
		}
	}
	return directACE && childACE
}

// authDirSIDLengthFitsACE reports whether the ACE contains the complete SID.
// The caller must verify that the eight-byte SID header is present before
// reading subAuthorityCount.
func authDirSIDLengthFitsACE(aceSize uint16, sidOffset uintptr, subAuthorityCount uint8) bool {
	if sidOffset > uintptr(aceSize) {
		return false
	}
	sidSize := uintptr(8) + uintptr(subAuthorityCount)*4
	return sidSize <= uintptr(aceSize)-sidOffset
}
