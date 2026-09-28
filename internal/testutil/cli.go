// Package testutil provides native subprocess fixtures for agent tests.
package testutil

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// CLI describes a fake CLI without requiring a shell or an installed agent.
type CLI struct {
	Args                            []string
	Output, Stderr                  string
	ExitCode                        int
	CountPath, GatePath, RequireEnv string
	Initial, Reply, ReplyOn         string
	ReadStdin, WaitEOF              bool
}

// NewCLI copies the current test binary. The package's TestMain must call
// RunCLI before m.Run. Each child has its own immutable sidecar configuration.
func NewCLI(t *testing.T, cfg CLI) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := "fake-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.Link(exe, dst); err != nil {
		data, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst+".fixture.json", data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dst
}

// RunCLI handles fixture subprocesses and returns only in the parent test.
func RunCLI() {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	data, err := os.ReadFile(exe + ".fixture.json")
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		panic(err)
	}
	var cfg CLI
	if err := json.Unmarshal(data, &cfg); err != nil {
		panic(err)
	}
	for i, arg := range cfg.Args {
		if len(os.Args) <= i+1 || os.Args[i+1] != arg {
			fmt.Fprintln(os.Stderr, "unexpected arguments:", os.Args[1:])
			os.Exit(2)
		}
	}
	if cfg.CountPath != "" {
		data, err := os.ReadFile(cfg.CountPath)
		if err != nil && !os.IsNotExist(err) {
			panic(err)
		}
		count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if err := os.WriteFile(cfg.CountPath, []byte(strconv.Itoa(count+1)), 0o600); err != nil {
			panic(err)
		}
	}
	if cfg.GatePath != "" {
		for {
			_, err := os.Stat(cfg.GatePath)
			if err == nil {
				break
			}
			if !os.IsNotExist(err) {
				panic(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if cfg.RequireEnv != "" && os.Getenv(cfg.RequireEnv) == "" {
		os.Exit(0)
	}
	if cfg.Initial != "" {
		fmt.Fprintln(os.Stdout, cfg.Initial)
	}
	if cfg.WaitEOF {
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			panic(err)
		}
	} else if cfg.ReadStdin {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			if cfg.Reply != "" && strings.Contains(scanner.Text(), cfg.ReplyOn) {
				fmt.Fprintln(os.Stdout, cfg.Reply)
			}
		}
		if err := scanner.Err(); err != nil {
			panic(err)
		}
	}
	fmt.Fprint(os.Stdout, cfg.Output)
	fmt.Fprint(os.Stderr, cfg.Stderr)
	os.Exit(cfg.ExitCode)
}
