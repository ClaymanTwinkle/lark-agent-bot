//go:build !windows

package main

// systemUILanguage returns "": on Linux and macOS the locale environment
// variables (LC_ALL, LC_MESSAGES, LANG) already carry the user's language.
func systemUILanguage() string {
	return ""
}
