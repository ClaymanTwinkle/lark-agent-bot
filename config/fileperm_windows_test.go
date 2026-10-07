//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// fileSDDL returns the DACL of path in SDDL form.
func fileSDDL(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%s): %v", path, err)
	}
	return sd.String()
}

func currentUserSID(t *testing.T) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser: %v", err)
	}
	return user.User.Sid.String()
}

// broadSIDs are the groups a config file must not grant access to.
var broadSIDs = []string{";;;AU)", ";;;BU)", ";;;WD)"}

// shareDirWithAuthenticatedUsers gives dir an inheritable ACL that grants
// Authenticated Users modify rights on new files, like a directory created
// outside the user profile on a default Windows install.
func shareDirWithAuthenticatedUsers(t *testing.T, dir string) {
	t.Helper()
	sddl := fmt.Sprintf("D:P(A;OICI;FA;;;%s)(A;OICI;FA;;;SY)(A;OICI;0x1301bf;;;AU)", currentUserSID(t))
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatalf("SecurityDescriptorFromString: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("DACL: %v", err)
	}
	err = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	if err != nil {
		t.Fatalf("SetNamedSecurityInfo(%s): %v", dir, err)
	}

	// The setup must take effect, or the test below proves nothing.
	probe := filepath.Join(dir, "probe.txt")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	if got := fileSDDL(t, probe); !strings.Contains(got, ";;;AU)") {
		t.Fatalf("probe file did not inherit the Authenticated Users entry: %s", got)
	}
}

func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	got := fileSDDL(t, path)
	if !strings.HasPrefix(got, "D:P") {
		t.Errorf("%s: DACL is not protected: %s", filepath.Base(path), got)
	}
	for _, sid := range broadSIDs {
		if strings.Contains(got, sid) {
			t.Errorf("%s: DACL grants a broad group (%s): %s", filepath.Base(path), sid, got)
		}
	}
	if want := currentUserACE(t); !strings.Contains(got, want) {
		t.Errorf("%s: DACL lacks the current user's entry %s: %s", filepath.Base(path), want, got)
	}
}

// currentUserACE returns the full-control entry for the current user as SDDL
// writes it. SDDL abbreviates well-known accounts, so the raw SID string is
// not always what appears: the built-in Administrator the CI runner uses is
// written as "LA".
func currentUserACE(t *testing.T) string {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:(A;;FA;;;" + currentUserSID(t) + ")")
	if err != nil {
		t.Fatalf("SecurityDescriptorFromString: %v", err)
	}
	return strings.TrimPrefix(sd.String(), "D:")
}

func TestSaveConfigRestrictsACLOnWindows(t *testing.T) {
	dir := t.TempDir()
	shareDirWithAuthenticatedUsers(t, dir)

	oldPath := ConfigPath
	ConfigPath = filepath.Join(dir, "config.toml")
	t.Cleanup(func() { ConfigPath = oldPath })

	if err := os.WriteFile(ConfigPath, []byte("language = \"en\"\n"), 0o600); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	cfg := &Config{Language: "zh"}
	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	assertOwnerOnly(t, ConfigPath)

	data, err := os.ReadFile(ConfigPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(data), `language = "zh"`) {
		t.Fatalf("saved config lost its content: %q", data)
	}

	// The other rewrite paths go through the same temp-file step.
	if err := writeRawConfig("language = \"ja\"\n"); err != nil {
		t.Fatalf("writeRawConfig: %v", err)
	}
	assertOwnerOnly(t, ConfigPath)

	unformatted := filepath.Join(dir, "unformatted.toml")
	if err := os.WriteFile(unformatted, []byte("language = \"en\"\n[log]\nlevel = \"info\"\n"), 0o600); err != nil {
		t.Fatalf("write unformatted config: %v", err)
	}
	if err := FormatConfigFile(unformatted); err != nil {
		t.Fatalf("FormatConfigFile: %v", err)
	}
	assertOwnerOnly(t, unformatted)
}

func TestRestrictFileToOwnerOnWindows(t *testing.T) {
	dir := t.TempDir()
	shareDirWithAuthenticatedUsers(t, dir)
	path := filepath.Join(dir, "secret.toml")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := RestrictFileToOwner(path); err != nil {
		t.Fatalf("RestrictFileToOwner: %v", err)
	}
	assertOwnerOnly(t, path)
}
