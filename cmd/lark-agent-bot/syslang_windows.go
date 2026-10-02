//go:build windows

package main

import "golang.org/x/sys/windows"

// systemUILanguage returns the user's Windows display language, e.g.
// "zh-CN", or "" when it cannot be read. Windows sets no LANG, so this is how
// the CLI learns the language of a user who has not set one in the config.
func systemUILanguage() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return ""
	}
	return langs[0]
}
