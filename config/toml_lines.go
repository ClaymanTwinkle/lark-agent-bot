package config

import (
	"fmt"
	"strings"
)

// Line-level TOML edits change one key at a time and leave every other line
// alone, so comments, blank lines and key order survive. Re-encoding the
// whole Config (saveConfig) loses them. Each helper takes the line range of
// one table's own keys: from the line after its header to the line before
// the next header.

// tomlValueEnd returns the last line of the key/value pair starting at line
// i: i itself, unless the value is an array or multi-line string that
// continues on later lines.
func tomlValueEnd(lines []string, i int) int {
	_, value, ok := strings.Cut(lines[i], "=")
	if !ok {
		return i
	}
	value = strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(value, `"""`) || strings.HasPrefix(value, `'''`):
		delim := value[:3]
		if strings.Count(value, delim) >= 2 {
			return i
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.Contains(lines[j], delim) {
				return j
			}
		}
	case strings.HasPrefix(value, "["):
		depth := bracketDepthChange(value)
		for j := i + 1; depth > 0 && j < len(lines); j++ {
			depth += bracketDepthChange(lines[j])
			if depth <= 0 {
				return j
			}
		}
		if depth <= 0 {
			return i
		}
	default:
		return i
	}
	return len(lines) - 1
}

// bracketDepthChange counts "[" minus "]" in s outside strings and comments.
func bracketDepthChange(s string) int {
	depth := 0
	var quote byte
	for k := 0; k < len(s); k++ {
		c := s[k]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				k++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return depth
		case c == '[':
			depth++
		case c == ']':
			depth--
		}
	}
	return depth
}

// upsertKeyInRange sets key = rawValue among lines[start..end]. An existing
// key, even a multi-line array, becomes one line that keeps its indentation
// and trailing comment. A new key goes after the last key in the range, so
// comments that introduce the next table stay above it.
func upsertKeyInRange(lines []string, start, end int, key, rawValue string) []string {
	start = max(start, 0)
	insertAt := start
	for i := start; i <= end && i < len(lines); i++ {
		if matchTomlStringKey(lines[i], key) {
			last := tomlValueEnd(lines, i)
			line := leadingWhitespace(lines[i]) + key + " = " + rawValue
			if comment := extractLineComment(lines[last]); comment != "" {
				line += " " + comment
			}
			out := append(lines[:i:i], line)
			return append(out, lines[last+1:]...)
		}
		if t := strings.TrimSpace(lines[i]); t != "" && !strings.HasPrefix(t, "#") {
			insertAt = i + 1
		}
	}
	return insertLines(lines, insertAt, []string{key + " = " + rawValue})
}

// removeKeyInRange deletes key and all lines of its value from
// lines[start..end], if present.
func removeKeyInRange(lines []string, start, end int, key string) []string {
	for i := max(start, 0); i <= end && i < len(lines); i++ {
		if matchTomlStringKey(lines[i], key) {
			return append(lines[:i:i], lines[tomlValueEnd(lines, i)+1:]...)
		}
	}
	return lines
}

// topLevelKeysEnd returns the line before the first table header: top-level
// keys must come before it.
func topLevelKeysEnd(lines []string) int {
	for i := range lines {
		if isAnyTableHeader(lines[i]) {
			return i - 1
		}
	}
	return len(lines) - 1
}

// upsertSectionKey sets key in the top-level [section] table, adding the
// table before the first table header when it is missing, as
// patchSectionField does.
func upsertSectionKey(lines []string, section, key, rawValue string) []string {
	header := "[" + section + "]"
	for i := range lines {
		if !matchTableHeader(lines[i], header) {
			continue
		}
		end := len(lines) - 1
		for j := i + 1; j < len(lines); j++ {
			if isAnyTableHeader(lines[j]) {
				end = j - 1
				break
			}
		}
		return upsertKeyInRange(lines, i+1, end, key, rawValue)
	}
	return insertLines(lines, topLevelKeysEnd(lines)+1, []string{"", header, key + " = " + rawValue})
}

// projectKeysEnd returns the last line of a project's own keys: the line
// before its first sub-table header, or the end of the project.
func projectKeysEnd(lines []string, span rawProjectSpan) int {
	for ln := span.start + 1; ln <= span.end; ln++ {
		if isAnyTableHeader(lines[ln]) {
			return ln - 1
		}
	}
	return span.end
}

// upsertProjectKey sets key directly under the projectIdx-th [[projects]].
func upsertProjectKey(lines []string, projectIdx int, key, rawValue string) []string {
	span := buildRawProjectSpans(lines)[projectIdx]
	return upsertKeyInRange(lines, span.start+1, projectKeysEnd(lines, span), key, rawValue)
}

// ensurePlatformOptions adds a [projects.platforms.options] table to the
// platformIdx-th platform of the projectIdx-th project when it has none.
func ensurePlatformOptions(lines []string, projectIdx, platformIdx int) []string {
	ps := buildRawProjectSpans(lines)[projectIdx].platforms[platformIdx]
	if ps.optionsStart >= 0 {
		return lines
	}
	at := ps.start + 1
	for i := ps.start + 1; i <= ps.end && i < len(lines); i++ {
		if t := strings.TrimSpace(lines[i]); t != "" && !strings.HasPrefix(t, "#") {
			at = i + 1
		}
	}
	return insertLines(lines, at, []string{"", "[projects.platforms.options]"})
}

func tomlBool(v bool) string { return fmt.Sprintf("%t", v) }
