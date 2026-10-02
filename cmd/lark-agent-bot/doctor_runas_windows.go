//go:build windows

package main

import (
	"fmt"
	"os"
)

// runDoctorUserIsolation stands in for the run_as_user audit, which needs
// sudo and Unix users.
func runDoctorUserIsolation(args []string) {
	if helpRequested(args) {
		fmt.Print(doctorUsage)
		return
	}
	fmt.Fprintln(os.Stderr, "doctor user-isolation: run_as_user is not supported on Windows")
	os.Exit(1)
}
