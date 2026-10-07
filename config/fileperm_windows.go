//go:build windows

package config

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// ownerOnlySDDL is a protected DACL (no inherited entries) that grants full
// control to the given user SID, SYSTEM and the Administrators group.
const ownerOnlySDDL = "D:P(A;;FA;;;%s)(A;;FA;;;SY)(A;;FA;;;BA)"

func restrictFileToOwner(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("config: get current user: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf(ownerOnlySDDL, user.User.Sid.String()))
	if err != nil {
		return fmt.Errorf("config: build security descriptor: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("config: read DACL: %w", err)
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
	if err != nil {
		return fmt.Errorf("config: set DACL on %s: %w", path, err)
	}
	return nil
}
