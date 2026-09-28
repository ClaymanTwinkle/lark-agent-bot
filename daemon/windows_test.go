//go:build windows

package daemon

import (
	"golang.org/x/sys/windows"
	"os"
	"strings"
	"testing"
)

func TestStrictPowerShellStopsOnCmdletErrors(t *testing.T) {
	script := strictPowerShell("Write-Output 'ok'")
	if !strings.HasPrefix(script, "$ErrorActionPreference = 'Stop'\n") {
		t.Fatalf("strictPowerShell() missing stop prelude:\n%s", script)
	}
	if !strings.Contains(script, "Write-Output 'ok'") {
		t.Fatalf("strictPowerShell() missing original script:\n%s", script)
	}
}

func TestBuildWindowsTaskScript(t *testing.T) {
	cfg := Config{
		BinaryPath: `C:\Program Files\lark-connect\lark-connect.exe`,
		WorkDir:    `C:\Users\me\.lark-connect`,
		LogFile:    `C:\Users\me\.lark-connect\logs\lark-connect.log`,
		LogMaxSize: 10 * 1024 * 1024,
		EnvPATH:    `C:\Program Files\nodejs;C:\Users\me\AppData\Local\Programs`,
		EnvExtra: map[string]string{
			"HTTPS_PROXY": "http://127.0.0.1:7890",
			"http_proxy":  "http://127.0.0.1:7890",
		},
	}

	script := buildWindowsTaskScript(cfg)
	for _, want := range []string{
		`$env:CC_LOG_FILE = 'C:\Users\me\.lark-connect\logs\lark-connect.log'`,
		`$env:CC_LOG_MAX_SIZE = '10485760'`,
		`$env:PATH = 'C:\Program Files\nodejs;C:\Users\me\AppData\Local\Programs'`,
		`$env:HTTPS_PROXY = 'http://127.0.0.1:7890'`,
		`$env:http_proxy = 'http://127.0.0.1:7890'`,
		`Set-Location -LiteralPath 'C:\Users\me\.lark-connect'`,
		`while ($true) {`,
		`& 'C:\Program Files\lark-connect\lark-connect.exe'`,
		`if ($exitCode -eq 0) { exit 0 }`,
		`Start-Sleep -Seconds 10`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

func TestWindowsTaskActionRunsHidden(t *testing.T) {
	got := windowsTaskAction(`C:\Users\me\.lark-connect\lark-connect-daemon.ps1`)
	for _, want := range []string{
		`powershell.exe`,
		`-WindowStyle Hidden`,
		`-NoProfile`,
		`-NonInteractive`,
		`-ExecutionPolicy Bypass`,
		`-File "C:\Users\me\.lark-connect\lark-connect-daemon.ps1"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("windowsTaskAction() missing %q: %q", want, got)
		}
	}
}

func TestWindowsTaskCreateUsesLimitedInteractivePrincipal(t *testing.T) {
	orig := runPowerShell
	t.Cleanup(func() { runPowerShell = orig })

	var script string
	runPowerShell = func(s string) (string, error) {
		script = s
		return "", nil
	}

	if err := createWindowsTask(`C:\Users\me\.lark-connect\lark-connect-daemon.ps1`); err != nil {
		t.Fatalf("createWindowsTask() error = %v", err)
	}
	for _, want := range []string{
		`New-ScheduledTaskAction`,
		`Register-ScheduledTask`,
		`-LogonType Interactive`,
		`-RunLevel Limited`,
		`-WindowStyle Hidden`,
		`C:\Users\me\.lark-connect\lark-connect-daemon.ps1`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("create script missing %q:\n%s", want, script)
		}
	}
}

func TestWindowsTaskMatchesActionRequiresExactAction(t *testing.T) {
	orig := runPowerShell
	t.Cleanup(func() { runPowerShell = orig })

	var script string
	runPowerShell = func(s string) (string, error) {
		script = s
		return "true", nil
	}

	if !windowsTaskMatchesAction(`C:\Users\me\.lark-connect\lark-connect-daemon.ps1`) {
		t.Fatal("windowsTaskMatchesAction() = false, want true")
	}
	for _, want := range []string{
		`$expectedArgs = '-WindowStyle Hidden -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "C:\Users\me\.lark-connect\lark-connect-daemon.ps1"'`,
		`$action.Execute -ieq 'powershell.exe'`,
		`$action.Arguments -eq $expectedArgs`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("reuse check script missing %q:\n%s", want, script)
		}
	}
}

func TestPowerShellLiteralEscapesSingleQuotes(t *testing.T) {
	got := powerShellLiteral(`C:\Users\O'Brien\.lark-connect`)
	want := `'C:\Users\O''Brien\.lark-connect'`
	if got != want {
		t.Fatalf("powerShellLiteral() = %q, want %q", got, want)
	}
}

func TestBuildWindowsTaskScript_DropsInvalidEnvName(t *testing.T) {
	cfg := Config{
		BinaryPath: "x", WorkDir: "y", LogFile: "l", LogMaxSize: 1, EnvPATH: "p",
		EnvExtra: map[string]string{"FOO BAR": "v", "OK": "ok"},
	}
	script := buildWindowsTaskScript(cfg)
	if strings.Contains(script, "FOO BAR") {
		t.Errorf("invalid env name leaked: %s", script)
	}
	if !strings.Contains(script, "$env:OK = 'ok'") {
		t.Errorf("valid env missing: %s", script)
	}
}

func TestBuildWindowsTaskScript_DropsEmptyValue(t *testing.T) {
	cfg := Config{
		BinaryPath: "x", WorkDir: "y", LogFile: "l", LogMaxSize: 1, EnvPATH: "p",
		EnvExtra: map[string]string{"EMPTY": "", "OK": "ok"},
	}
	script := buildWindowsTaskScript(cfg)
	if strings.Contains(script, "$env:EMPTY") {
		t.Errorf("empty value should be skipped: %s", script)
	}
}

// Windows exposes writable files as 0666 regardless of the requested POSIX
// mode. Verify the actual DACL when upgrading a broadly readable script.
func TestSchtasksInstall_TightensExistingScriptACL(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())

	orig := runPowerShell
	t.Cleanup(func() { runPowerShell = orig })
	runPowerShell = func(script string) (string, error) { return "", nil }

	if err := os.MkdirAll(DefaultDataDir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	scriptPath := windowsTaskScriptPath()
	if err := os.WriteFile(scriptPath, []byte("$env:OLD = 'leftover'\r\n"), 0o644); err != nil {
		t.Fatalf("seed legacy script: %v", err)
	}
	broad, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := broad.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(scriptPath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}

	mgr := &schtasksManager{}
	cfg := Config{
		BinaryPath: "C:\\cc.exe",
		WorkDir:    t.TempDir(),
		LogFile:    "C:\\cc.log",
		LogMaxSize: 1024,
		EnvPATH:    "C:\\bin",
		EnvExtra:   map[string]string{"CUSTOM_TOKEN": "captured"},
	}
	if err := mgr.Install(cfg); err != nil {
		t.Fatalf("Install: %v", err)
	}
	sd, err := windows.GetNamedSecurityInfo(scriptPath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	want := "D:P(A;;FA;;;" + user.User.Sid.String() + ")"
	// Windows may retain the AUTO_INHERITED bookkeeping flag even though
	// the protected DACL contains only our explicit current-user ACE.
	if got := strings.Replace(sd.String(), "D:PAI", "D:P", 1); got != want {
		t.Fatalf("script ACL = %q, want %q", got, want)
	}
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "leftover") || !strings.Contains(string(content), "$env:CUSTOM_TOKEN = 'captured'") {
		t.Fatalf("script not replaced: %s", content)
	}
}
