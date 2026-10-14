package state

// authDirDiagnosticSIDFitsACE reports whether an ACE contains a complete SID
// at sidOffset. The caller must already have verified that the eight-byte SID
// header is present before reading subAuthorityCount.
func authDirDiagnosticSIDFitsACE(aceSize uint16, sidOffset uintptr, subAuthorityCount uint8) bool {
	if sidOffset > uintptr(aceSize) {
		return false
	}
	sidSize := uintptr(8) + uintptr(subAuthorityCount)*4
	return sidSize <= uintptr(aceSize)-sidOffset
}
