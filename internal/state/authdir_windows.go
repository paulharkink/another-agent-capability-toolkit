//go:build windows

package state

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func secureAuthDir(path string) error {
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	var pin runtime.Pinner
	pin.Pin(tokenUser.User.Sid)
	defer pin.Unpin()

	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(tokenUser.User.Sid),
		},
	}}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
}

func authDirPrivacyState(path string) (string, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	if sd == nil {
		return "", fmt.Errorf("security descriptor is missing")
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return "", err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return "", err
	}
	if dacl == nil {
		return owner.String() + ":null", nil
	}
	control, _, err := sd.Control()
	if err != nil {
		return "", err
	}
	if dacl.AceCount == 0 {
		return fmt.Sprintf("%s:0:%x", owner.String(), control), nil
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		return "", err
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	return fmt.Sprintf("%s:%d:%s:%d:%d:%x:%x", owner.String(), dacl.AceCount, aceSID.String(), ace.Header.AceType, ace.Header.AceFlags, ace.Mask, control), nil
}

func authDirIsPrivate(path string) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false, err
	}
	if sd == nil {
		return false, fmt.Errorf("security descriptor is missing")
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return false, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	control, _, err := sd.Control()
	if err != nil {
		return false, err
	}
	// GetNamedSecurityInfo returns a self-relative descriptor with other
	// representation bits; only DACL protection determines inheritance here.
	policy := authDirACLPolicy{
		OwnerIsCurrentUser:    owner != nil && owner.Equals(tokenUser.User.Sid),
		OwnerIsAdministrators: owner != nil && owner.IsWellKnown(windows.WinBuiltinAdministratorsSid),
		DACLPresent:           dacl != nil,
		DACLProtected:         control&windows.SE_DACL_PROTECTED != 0,
	}
	if dacl == nil || dacl.AceCount != 2 {
		return authDirACLIsPrivate(policy), nil
	}
	policy.ACEs = make([]authDirACEPolicy, 0, dacl.AceCount)
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return false, err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			policy.ACEs = append(policy.ACEs, authDirACEPolicy{Type: ace.Header.AceType, Flags: ace.Header.AceFlags})
			continue
		}
		const maskEnd = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.Mask) + unsafe.Sizeof(windows.ACCESS_ALLOWED_ACE{}.Mask)
		const sidOffset = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
		if uintptr(ace.Header.AceSize) < maskEnd || uintptr(ace.Header.AceSize) < sidOffset+8 {
			policy.ACEs = append(policy.ACEs, authDirACEPolicy{Type: ace.Header.AceType, Flags: ace.Header.AceFlags})
			continue
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		sidHeader := unsafe.Slice((*byte)(unsafe.Pointer(&ace.SidStart)), 8)
		if !authDirSIDLengthFitsACE(ace.Header.AceSize, sidOffset, sidHeader[1]) || !aceSID.IsValid() {
			policy.ACEs = append(policy.ACEs, authDirACEPolicy{Type: ace.Header.AceType, Flags: ace.Header.AceFlags})
			continue
		}
		policy.ACEs = append(policy.ACEs, authDirACEPolicy{
			Type:             ace.Header.AceType,
			Flags:            ace.Header.AceFlags,
			Mask:             uint32(ace.Mask),
			SIDKnown:         true,
			SIDIsCurrentUser: aceSID.Equals(tokenUser.User.Sid),
		})
	}
	return authDirACLIsPrivate(policy), nil
}
