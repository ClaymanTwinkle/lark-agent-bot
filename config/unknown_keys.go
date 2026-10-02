package config

import (
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
)

// decodeConfig parses config.toml data into cfg and returns the keys that no
// Config field took (see unknownKeys).
func decodeConfig(data []byte, cfg *Config) ([]string, error) {
	md, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, err
	}
	return unknownKeys(md), nil
}

// unknownKeys returns the keys of a decoded config that no Config field took,
// as dotted paths such as "log.idle_timeout_mins", in file order and without
// duplicates. Such a key is misspelled or sits under the wrong table header,
// typically a top-level key written below [log]; TOML has no way to end a
// table, so the decoder drops it without an error.
//
// Keys nested in a map[string]any value, such as
// [projects.agent.options.env], are kept by that map but never marked as
// decoded, so they are left out. A key under an unknown table is covered by
// the table and not listed on its own. Keys of [[array]] entries carry no
// index, so a key repeated in several [[projects]] is listed once.
func unknownKeys(md toml.MetaData) []string {
	configType := reflect.TypeOf(Config{})
	seen := map[string]bool{}
	var out []string
	for _, key := range md.Undecoded() {
		if _, inAny, _ := typeAt(configType, key); inAny || coveredBy(key, seen) {
			continue
		}
		s := key.String()
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// coveredBy reports whether a proper prefix of key is in reported.
func coveredBy(key toml.Key, reported map[string]bool) bool {
	for i := 1; i < len(key); i++ {
		if reported[key[:i].String()] {
			return true
		}
	}
	return false
}

// warnUnknownKeys logs one warning per unknown key of the config file at path.
func warnUnknownKeys(path string, keys []string) {
	for _, key := range keys {
		args := []any{"key", key, "file", path}
		if hint := unknownKeyHint(key); hint != "" {
			args = append(args, "hint", hint)
		}
		slog.Warn("config: unknown key ignored, check its spelling and the [table] it is under", args...)
	}
}

// unknownKeyHint suggests where an unknown key belongs when a table above it
// has a field of the same name, e.g. the top level for
// "log.idle_timeout_mins".
func unknownKeyHint(dotted string) string {
	// Config field names never need quoting, so a plain split is enough.
	key := toml.Key(strings.Split(dotted, "."))
	if len(key) < 2 {
		return ""
	}
	name := key[len(key)-1]
	configType := reflect.TypeOf(Config{})
	for i := len(key) - 2; i >= 0; i-- {
		candidate := append(append(toml.Key{}, key[:i]...), name)
		if !isStructField(configType, candidate) {
			continue
		}
		if i == 0 {
			return fmt.Sprintf("%s is a top-level key: put it above the first [table] header", name)
		}
		return fmt.Sprintf("did you mean %s?", candidate)
	}
	return ""
}

// typeAt follows key through t the way the TOML decoder does and returns the
// type it ends on. inAny is true when a part falls inside an `any` value,
// which keeps everything below it; ok is false when a part names no field.
func typeAt(t reflect.Type, key toml.Key) (end reflect.Type, inAny, ok bool) {
	for _, part := range key {
		t = elemType(t)
		switch t.Kind() {
		case reflect.Interface:
			return t, true, true
		case reflect.Map:
			t = t.Elem()
		case reflect.Struct:
			if t, ok = tomlFieldType(t, part); !ok {
				return nil, false, false
			}
		default:
			return nil, false, false
		}
	}
	return t, false, true
}

// isStructField reports whether key names a struct field, not a map entry,
// of t.
func isStructField(t reflect.Type, key toml.Key) bool {
	if len(key) == 0 {
		return false
	}
	parent, inAny, ok := typeAt(t, key[:len(key)-1])
	if !ok || inAny {
		return false
	}
	parent = elemType(parent)
	if parent.Kind() != reflect.Struct {
		return false
	}
	_, ok = tomlFieldType(parent, key[len(key)-1])
	return ok
}

// elemType strips pointers, slices and arrays: [[table]] keys carry no index.
func elemType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	return t
}

// tomlFieldType returns the type of the field of struct t that the decoder
// fills for key: the toml tag name, else the Go field name, matched exactly
// first and then case-insensitively, as the decoder does.
func tomlFieldType(t reflect.Type, key string) (reflect.Type, bool) {
	var fold reflect.Type
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("toml")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" && elemType(f.Type).Kind() == reflect.Struct {
			if ft, ok := tomlFieldType(elemType(f.Type), key); ok {
				return ft, true
			}
			continue
		}
		if name == "" {
			name = f.Name
		}
		if name == key {
			return f.Type, true
		}
		if fold == nil && strings.EqualFold(name, key) {
			fold = f.Type
		}
	}
	return fold, fold != nil
}

// checkRewritable refuses a rewrite of the config file at path from the Config
// struct when the file has keys no field took: saveConfig would delete them.
func checkRewritable(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config: %w", err)
	}
	keys, err := decodeConfig(data, &Config{})
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if len(keys) > 0 {
		return fmt.Errorf("config: %s has keys this version does not use (%s); saving rewrites the whole file and would delete them, so nothing was saved: fix their spelling or table, or remove them, then try again",
			path, strings.Join(keys, ", "))
	}
	return nil
}
