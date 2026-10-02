package main

import (
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/ClaymanTwinkle/lark-agent-bot/config"
	"github.com/ClaymanTwinkle/lark-agent-bot/core"
)

// cliLanguage is the language of the messages the CLI prints outside a chat
// (first-run hints, feishu setup, doctor) for the config file at configPath.
func cliLanguage(configPath string) core.Language {
	return resolveCLILanguage(configFileLanguage(configPath), os.Getenv, systemUILanguage)
}

// resolveCLILanguage picks the CLI language: the config's language, else the
// locale in LC_ALL / LC_MESSAGES / LANG, else the OS display language, else
// Chinese, which these messages used before any of this was read.
func resolveCLILanguage(configLang string, getenv func(string) string, systemLang func() string) core.Language {
	if lang := core.NormalizeLanguageString(strings.TrimSpace(configLang)); lang != core.LangAuto {
		return lang
	}
	// POSIX order: LC_ALL overrides LC_MESSAGES, which overrides LANG.
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := getenv(name); value != "" {
			if lang, ok := localeLanguage(value); ok {
				return lang
			}
		}
	}
	if lang, ok := localeLanguage(systemLang()); ok {
		return lang
	}
	return core.LangChinese
}

// localeLanguage maps a locale name such as "zh_CN.UTF-8", "zh-Hant-TW",
// "ja_JP" or "C" to a supported language. A locale in another language gives
// English; ok is false only when value names no locale at all.
func localeLanguage(value string) (core.Language, bool) {
	name := strings.TrimSpace(value)
	if i := strings.IndexAny(name, ".@"); i >= 0 {
		name = name[:i] // drop the codeset and modifier
	}
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' })
	if len(parts) == 0 {
		return "", false
	}
	switch strings.ToLower(parts[0]) {
	case "zh":
		traditional := false
		for _, p := range parts[1:] {
			switch strings.ToLower(p) {
			case "hans":
				return core.LangChinese, true
			case "hant", "tw", "hk", "mo":
				traditional = true
			}
		}
		if traditional {
			return core.LangTraditionalChinese, true
		}
		return core.LangChinese, true
	case "ja":
		return core.LangJapanese, true
	case "es":
		return core.LangSpanish, true
	default:
		// "en", "C", "POSIX" and languages without translations.
		return core.LangEnglish, true
	}
}

// configFileLanguage returns the top-level language of the config file at
// path, or "" when the file is missing, does not parse or does not set one.
// It reads only that key, so it works on a config that does not validate.
func configFileLanguage(path string) string {
	if path == "" {
		return ""
	}
	var c struct {
		Language string `toml:"language"`
	}
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return ""
	}
	return c.Language
}

// cliText renders key in the CLI language of the config file at configPath.
func cliText(configPath string, key core.MsgKey, args ...any) string {
	return core.NewI18n(cliLanguage(configPath)).Tf(key, args...)
}

// setupText renders key in the CLI language of the config in use: the one
// set with --config, else the default config file.
func setupText(key core.MsgKey, args ...any) string {
	path := config.ConfigPath
	if path == "" {
		path = resolveConfigPath("")
	}
	return cliText(path, key, args...)
}
