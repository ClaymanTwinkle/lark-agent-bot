package core

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type referenceKind string

const (
	referenceKindUnknown referenceKind = "unknown"
	referenceKindFile    referenceKind = "file"
	referenceKindDir     referenceKind = "dir"
)

type referenceLocationFormat string

const (
	referenceLocationNone         referenceLocationFormat = ""
	referenceLocationColonLine    referenceLocationFormat = "colon_line"
	referenceLocationColonLineCol referenceLocationFormat = "colon_line_col"
	referenceLocationColonRange   referenceLocationFormat = "colon_line_range"
	referenceLocationHashLine     referenceLocationFormat = "hash_line"
	referenceLocationHashLineCol  referenceLocationFormat = "hash_line_col"
)

type localReference struct {
	kind           referenceKind
	raw            string
	pathOriginal   string
	pathAbs        string
	pathRel        string
	isRelative     bool
	locationFormat referenceLocationFormat
	lineStart      int
	lineEnd        int
	column         int
}

var (
	reMarkdownLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)((?::\d+(?::\d+)?|:\d+-\d+)?)?`)
	reHashLocation   = regexp.MustCompile(`^(.*?)(#L(\d+)(?:C(\d+))?)$`)
	reColonLineCol   = regexp.MustCompile(`^(.*):(\d+):(\d+)$`)
	reColonLineRange = regexp.MustCompile(`^(.*):(\d+)-(\d+)$`)
	reColonLineOnly  = regexp.MustCompile(`^(.*):(\d+)$`)
)

func parseUserLocalReference(raw, workspaceDir string) (*localReference, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty reference")
	}
	if match := reMarkdownLink.FindStringSubmatch(raw); len(match) >= 3 && match[0] == raw {
		suffix := ""
		if len(match) >= 4 {
			suffix = match[3]
		}
		raw = match[2] + suffix
	}
	ref, ok := parseLocalReference(raw, workspaceDir)
	if !ok {
		return nil, fmt.Errorf("cannot parse local reference")
	}
	return ref, nil
}

func parseLocalReference(raw, workspaceDir string) (*localReference, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || isWebURL(raw) || strings.HasPrefix(raw, "//") {
		return nil, false
	}
	ref := &localReference{raw: raw}
	pathPart := raw
	switch {
	case reHashLocation.MatchString(pathPart):
		m := reHashLocation.FindStringSubmatch(pathPart)
		pathPart = m[1]
		ref.lineStart = atoiSafe(m[3])
		ref.column = atoiSafe(m[4])
		if ref.column > 0 {
			ref.locationFormat = referenceLocationHashLineCol
		} else {
			ref.locationFormat = referenceLocationHashLine
		}
	case reColonLineCol.MatchString(pathPart):
		m := reColonLineCol.FindStringSubmatch(pathPart)
		pathPart = m[1]
		ref.lineStart = atoiSafe(m[2])
		ref.column = atoiSafe(m[3])
		ref.locationFormat = referenceLocationColonLineCol
	case reColonLineRange.MatchString(pathPart):
		m := reColonLineRange.FindStringSubmatch(pathPart)
		pathPart = m[1]
		ref.lineStart = atoiSafe(m[2])
		ref.lineEnd = atoiSafe(m[3])
		ref.locationFormat = referenceLocationColonRange
	case reColonLineOnly.MatchString(pathPart):
		m := reColonLineOnly.FindStringSubmatch(pathPart)
		pathPart = m[1]
		ref.lineStart = atoiSafe(m[2])
		ref.locationFormat = referenceLocationColonLine
	}
	if strings.HasPrefix(pathPart, "file://") {
		u, err := url.Parse(pathPart)
		if err != nil || u.Path == "" {
			return nil, false
		}
		pathPart = u.Path
		// file:///C:/x has the path /C:/x.
		if hasDrivePrefix(pathPart[1:]) {
			pathPart = pathPart[1:]
		}
	}
	if strings.Contains(pathPart, `\`) && backslashIsSeparator(pathPart, ref.locationFormat != referenceLocationNone) {
		pathPart = strings.ReplaceAll(pathPart, `\`, "/")
	}
	if !looksLikeLocalPath(pathPart) {
		return nil, false
	}
	ref.pathOriginal = pathPart
	ref.isRelative = !isAbsReferencePath(pathPart)
	workspace := referenceWorkspace(workspaceDir)
	if ref.isRelative {
		if workspace != "" {
			ref.pathAbs = cleanReferencePath(workspace + "/" + pathPart)
			ref.pathRel = relReferencePath(workspace, ref.pathAbs)
		}
	} else {
		ref.pathAbs = cleanReferencePath(pathPart)
		if workspace != "" {
			ref.pathRel = relReferencePath(workspace, ref.pathAbs)
		}
	}
	ref.kind = inferReferenceKind(ref)
	return ref, true
}

// Reference paths are kept with forward slashes and handled the same on
// every host: "/root/x" and "D:\Projects\x" are both absolute, so an
// agent's output renders alike on Linux, macOS and Windows.

// hasDrivePrefix reports whether p starts with a Windows drive, as in
// "C:\" or "C:/".
func hasDrivePrefix(p string) bool {
	if len(p) < 3 || p[1] != ':' || (p[2] != '\\' && p[2] != '/') {
		return false
	}
	c := p[0]
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// backslashIsSeparator reports whether the backslashes in p separate the
// parts of a Windows path. They do in a drive path ("D:\x\y") and in a
// relative path that starts with ".\" or "..\", carries a location
// (hasLocation) or ends in a file name with an extension ("x\main.go").
// They do not in text that only resembles a path, such as "\n", a markdown
// escape ("foo\_bar") or "DOMAIN\user", nor in a UNC path ("\\server\x"),
// nor when p has a character Windows does not allow in a name.
func backslashIsSeparator(p string, hasLocation bool) bool {
	drive := hasDrivePrefix(p)
	rest := p
	if drive {
		rest = p[2:]
	} else if strings.HasPrefix(p, `\`) {
		return false
	}
	if strings.ContainsAny(rest, `:*?"<>|`) {
		return false
	}
	if drive || hasLocation || strings.HasPrefix(p, `.\`) || strings.HasPrefix(p, `..\`) {
		return true
	}
	name := p[strings.LastIndexAny(p, `\/`)+1:]
	return path.Ext(name) != ""
}

func isAbsReferencePath(p string) bool {
	return strings.HasPrefix(p, "/") || hasDrivePrefix(p)
}

// referenceWorkspace returns the workspace directory, a path on this host,
// in the form parseLocalReference compares references with.
func referenceWorkspace(dir string) string {
	if dir == "" {
		return ""
	}
	if hasDrivePrefix(dir) {
		dir = strings.ReplaceAll(dir, `\`, "/")
	} else {
		dir = filepath.ToSlash(dir)
	}
	return cleanReferencePath(dir)
}

// cleanReferencePath is path.Clean that keeps a drive: ".." stops at "D:/".
func cleanReferencePath(p string) string {
	if hasDrivePrefix(p) {
		return p[:2] + path.Clean(p[2:])
	}
	return path.Clean(p)
}

// relReferencePath returns target relative to base, both cleaned, or ""
// when target is neither base nor below it. Drive paths compare
// case-insensitively, as Windows does.
func relReferencePath(base, target string) string {
	same := func(a, b string) bool { return a == b }
	if hasDrivePrefix(base) {
		same = strings.EqualFold
	}
	if same(base, target) {
		return "."
	}
	prefix := base
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if len(target) > len(prefix) && same(target[:len(prefix)], prefix) {
		return target[len(prefix):]
	}
	return ""
}

func inferReferenceKind(ref *localReference) referenceKind {
	if ref == nil {
		return referenceKindUnknown
	}
	if ref.pathAbs != "" {
		if info, err := os.Stat(ref.pathAbs); err == nil {
			if info.IsDir() {
				return referenceKindDir
			}
			return referenceKindFile
		}
	}
	if ref.locationFormat != referenceLocationNone {
		return referenceKindFile
	}
	if strings.HasSuffix(ref.pathOriginal, "/") {
		return referenceKindDir
	}
	base := path.Base(strings.TrimSuffix(ref.pathOriginal, "/"))
	if path.Ext(base) != "" {
		return referenceKindFile
	}
	return referenceKindUnknown
}

func looksLikeLocalPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") || strings.HasPrefix(p, "//") {
		return false
	}
	switch {
	case strings.HasPrefix(p, "/"):
		return true
	case strings.HasPrefix(p, "./"), strings.HasPrefix(p, "../"):
		return true
	case strings.Contains(p, "/"):
		return true
	default:
		return strings.Contains(path.Base(p), ".")
	}
}

func isWebURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func atoiSafe(s string) int {
	if s == "" {
		return 0
	}
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
