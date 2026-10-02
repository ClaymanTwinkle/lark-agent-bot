package main

import "testing"

func TestSupervisorRestartExitCode(t *testing.T) {
	tests := []struct {
		in       string
		wantCode int
		wantOK   bool
	}{
		{"75", 75, true},
		{" 75 ", 75, true},
		{"1", 1, true},
		{"255", 255, true},
		{"", 0, false},
		{"0", 0, false},
		{"256", 0, false},
		{"-1", 0, false},
		{"yes", 0, false},
	}
	for _, tt := range tests {
		code, ok := supervisorRestartExitCode(tt.in)
		if code != tt.wantCode || ok != tt.wantOK {
			t.Errorf("supervisorRestartExitCode(%q) = %d, %v; want %d, %v", tt.in, code, ok, tt.wantCode, tt.wantOK)
		}
	}
}
