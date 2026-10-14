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
	state, err := authDirPrivacyState(path)
	if err != nil {
		return false, err
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false, err
	}
	return state == fmt.Sprintf("%s:1:%s:%d:3:%x:%x", tokenUser.User.Sid.String(), tokenUser.User.Sid.String(), windows.ACCESS_ALLOWED_ACE_TYPE, windows.GENERIC_ALL, windows.SE_DACL_PROTECTED), nil
}
