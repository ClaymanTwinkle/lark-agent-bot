//go:build !windows

package config

// restrictFileToOwner is a no-op on Unix: secret files are created with mode
// 0600.
func restrictFileToOwner(string) error { return nil }
