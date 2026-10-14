//go:build windows

package state

import (
	"fmt"
	"runtime"
	"strings"
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

// authDirACLDiagnostic reports only principal classes and ACE metadata. It is
// intended for test failures and deliberately omits arbitrary SID values.
func authDirACLDiagnostic(path string) (string, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	if sd == nil {
		return "security descriptor missing", nil
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return "", err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return "", err
	}
	control, _, err := sd.Control()
	if err != nil {
		return "", err
	}
	ownerIsUser := owner != nil && owner.Equals(tokenUser.User.Sid)
	ownerIsAdmins := owner != nil && owner.IsWellKnown(windows.WinBuiltinAdministratorsSid)
	ownerIsSystem := owner != nil && owner.IsWellKnown(windows.WinLocalSystemSid)
	daclProtected := control&windows.SE_DACL_PROTECTED != 0
	if dacl == nil {
		return fmt.Sprintf("ownerUser=%t ownerAdministrators=%t ownerSystem=%t daclPresent=false daclProtected=%t aceCount=0", ownerIsUser, ownerIsAdmins, ownerIsSystem, daclProtected), nil
	}
	aces := make([]string, 0, dacl.AceCount)
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return "", err
		}
		const maskEnd = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.Mask) + unsafe.Sizeof(windows.ACCESS_ALLOWED_ACE{}.Mask)
		if uintptr(ace.Header.AceSize) < maskEnd {
			return "", fmt.Errorf("ACE %d is too short to contain an access mask", index)
		}
		sidKnown := false
		aceIsOwner, aceIsUser := false, false
		aceIsAdmins, aceIsSystem := false, false
		aceIsUsers, aceIsAuthenticated, aceIsEveryone := false, false, false
		if ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			const sidOffset = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
			if uintptr(ace.Header.AceSize) >= sidOffset+8 { // A SID header is eight bytes.
				aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
				sidHeader := unsafe.Slice((*byte)(unsafe.Pointer(&ace.SidStart)), 8)
				if authDirDiagnosticSIDFitsACE(ace.Header.AceSize, sidOffset, sidHeader[1]) {
					sidKnown = aceSID.IsValid()
				}
				if sidKnown {
					aceIsOwner = owner != nil && aceSID.Equals(owner)
					aceIsUser = aceSID.Equals(tokenUser.User.Sid)
					aceIsAdmins = aceSID.IsWellKnown(windows.WinBuiltinAdministratorsSid)
					aceIsSystem = aceSID.IsWellKnown(windows.WinLocalSystemSid)
					aceIsUsers = aceSID.IsWellKnown(windows.WinBuiltinUsersSid)
					aceIsAuthenticated = aceSID.IsWellKnown(windows.WinAuthenticatedUserSid)
					aceIsEveryone = aceSID.IsWellKnown(windows.WinWorldSid)
				}
			}
		}
		aces = append(aces, fmt.Sprintf("type=%d flags=0x%x mask=0x%x sidKnown=%t owner=%t user=%t administrators=%t system=%t users=%t authenticated=%t everyone=%t", ace.Header.AceType, ace.Header.AceFlags, ace.Mask, sidKnown, aceIsOwner, aceIsUser, aceIsAdmins, aceIsSystem, aceIsUsers, aceIsAuthenticated, aceIsEveryone))
	}
	return fmt.Sprintf("ownerUser=%t ownerAdministrators=%t ownerSystem=%t daclPresent=true daclProtected=%t aceCount=%d aces=[%s]", ownerIsUser, ownerIsAdmins, ownerIsSystem, daclProtected, dacl.AceCount, strings.Join(aces, "; ")), nil
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
	if owner == nil || owner.String() != tokenUser.User.Sid.String() || dacl == nil || dacl.AceCount != 1 {
		return false, nil
	}
	control, _, err := sd.Control()
	if err != nil {
		return false, err
	}
	// GetNamedSecurityInfo returns a self-relative descriptor with other
	// representation bits; only DACL protection determines inheritance here.
	if control&windows.SE_DACL_PROTECTED == 0 {
		return false, nil
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		return false, err
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	return aceSID.String() == owner.String() && ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE && ace.Header.AceFlags == windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE && ace.Mask == windows.GENERIC_ALL, nil
}
