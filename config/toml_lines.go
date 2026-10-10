package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
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

// The writers that add or remove a whole element of an array of tables (a
// command, alias, provider, project or platform) edit that element's block
// of lines and leave the rest of the file alone.

// tableBlock is one element of an array of tables: from its [[name]] header
// to the end of its keys and sub-tables (see sectionEnd).
type tableBlock struct{ start, end int }

// tableHeaders returns the index of every table header line, skipping the
// continuation lines of multi-line values, which may start with "[".
func tableHeaders(lines []string) []int {
	var headers []int
	for i := 0; i < len(lines); i++ {
		switch t := strings.TrimSpace(lines[i]); {
		case t == "" || strings.HasPrefix(t, "#"):
		case strings.HasPrefix(t, "["):
			headers = append(headers, i)
		default:
			i = tomlValueEnd(lines, i)
		}
	}
	return headers
}

// tableHeaderName returns the dotted name in a header line and whether it
// is an array-of-tables header.
func tableHeaderName(line string) (name string, array bool) {
	t := strings.TrimSpace(line)
	open, closing := "[", "]"
	if strings.HasPrefix(t, "[[") {
		open, closing, array = "[[", "]]", true
	}
	end := strings.Index(t, closing)
	if end < len(open) {
		return "", false
	}
	return strings.TrimSpace(t[len(open):end]), array
}

// inTable reports whether a header named header is table name or one of
// its sub-tables.
func inTable(header, name string) bool {
	return header == name || strings.HasPrefix(header, name+".")
}

// sectionEnd returns the last line of the table text in lines[from..to]:
// the last line that is neither blank nor a comment (or from, when there is
// none), plus the comments right below it, such as commented-out keys. A
// comment run that reaches the next header with no blank line in between
// introduces that header instead, and so do comments after a blank line.
func sectionEnd(lines []string, from, to int) int {
	end := from
	for i := min(to, len(lines)-1); i > from; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" && !strings.HasPrefix(t, "#") {
			end = i
			break
		}
	}
	next := end + 1
	for next <= to && next < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[next]), "#") {
		next++
	}
	if next < len(lines) && isAnyTableHeader(lines[next]) {
		return end
	}
	return next - 1
}

// nextHeader returns the first table header after line i, or len(lines).
func nextHeader(lines []string, i int) int {
	for _, h := range tableHeaders(lines) {
		if h > i {
			return h
		}
	}
	return len(lines)
}

// tableBlocks returns the [[name]] elements whose headers lie in
// lines[from..to]. An element ends before the next header outside it, or
// at to.
func tableBlocks(lines []string, from, to int, name string) []tableBlock {
	headers := tableHeaders(lines)
	var blocks []tableBlock
	for k, h := range headers {
		if h < from || h > to {
			continue
		}
		if n, array := tableHeaderName(lines[h]); !array || n != name {
			continue
		}
		last := to
		for _, next := range headers[k+1:] {
			if n, _ := tableHeaderName(lines[next]); next > to || !strings.HasPrefix(n, name+".") {
				last = min(next-1, to)
				break
			}
		}
		blocks = append(blocks, tableBlock{h, sectionEnd(lines, h, last)})
	}
	return blocks
}

// tableEnd returns where the last of the tables in lines[from..to] that are
// name or its sub-tables ends (see sectionEnd), or -1 when there are none.
func tableEnd(lines []string, from, to int, name string) int {
	headers := tableHeaders(lines)
	end := -1
	for k, h := range headers {
		if h < from || h > to {
			continue
		}
		if n, _ := tableHeaderName(lines[h]); !inTable(n, name) {
			continue
		}
		last := to
		if k+1 < len(headers) {
			last = min(headers[k+1]-1, to)
		}
		end = sectionEnd(lines, h, last)
	}
	return end
}

// topLevelTableAt returns where the first element of a new top-level array
// goes: after the table before the first [[projects]], so the comments
// above that project stay with it, or at the end of the file.
func topLevelTableAt(lines []string) int {
	for _, h := range tableHeaders(lines) {
		if n, array := tableHeaderName(lines[h]); array && n == "projects" {
			return sectionEnd(lines, -1, h-1) + 1
		}
	}
	return len(lines)
}

// encodeTableBlock renders v as one [[name]] element, the way saveConfig
// writes it, minus what only clutters the file: keys of the element's own
// table set to "", which decode to the same empty field, and empty tables,
// which decode like missing ones (see equalIgnoringEmpty). formatTOML drops
// empty tables when writing, but not one followed by a comment.
func encodeTableBlock(name string, v any) ([]string, error) {
	parent, last := "", name
	if i := strings.LastIndex(name, "."); i >= 0 {
		parent, last = name[:i+1], name[i+1:]
	}
	list := reflect.MakeSlice(reflect.SliceOf(reflect.TypeOf(v)), 1, 1)
	list.Index(0).Set(reflect.ValueOf(v))
	var buf strings.Builder
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(map[string]any{last: list.Interface()}); err != nil {
		return nil, fmt.Errorf("encode %s: %w", name, err)
	}
	encoded := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var block []string
	ownKeys := true
	for i, line := range encoded {
		switch {
		case strings.HasPrefix(line, "[["):
			ownKeys = block == nil
			line = "[[" + parent + line[2:]
		case strings.HasPrefix(line, "["):
			ownKeys = false
			if emptyTable(encoded[i+1:]) {
				continue
			}
			line = "[" + parent + line[1:]
		case ownKeys:
			if _, value, _ := strings.Cut(line, " = "); value == `""` {
				continue
			}
		}
		block = append(block, line)
	}
	return block, nil
}

// emptyTable reports whether rest, the encoder's lines after a [table]
// header, sets no key before the next header.
func emptyTable(rest []string) bool {
	for _, line := range rest {
		if line != "" {
			return strings.HasPrefix(line, "[")
		}
	}
	return true
}

// appendTableBlock adds v as a new [[name]] element after the last of
// blocks, or at line at when there are none. n is the number of elements
// the decoded config had; ok is false when blocks does not hold one block
// per element, as when the array is written inline.
func appendTableBlock(lines []string, blocks []tableBlock, n, at int, name string, v any) (edited []string, ok bool, err error) {
	if len(blocks) != n {
		return nil, false, nil
	}
	block, err := encodeTableBlock(name, v)
	if err != nil {
		return nil, false, err
	}
	if n > 0 {
		at = blocks[n-1].end + 1
	}
	// formatTOML collapses the blank lines when the file is written.
	return insertLines(lines, at, slices.Concat([]string{""}, block, []string{""})), true, nil
}

// removeTableBlocks deletes the blocks of the elements drop picks. Comments
// above a removed header stay: they may introduce the whole array rather
// than that element. n and ok are as for appendTableBlock.
func removeTableBlocks(lines []string, blocks []tableBlock, n int, drop func(k int) bool) (edited []string, ok bool) {
	if len(blocks) != n {
		return nil, false
	}
	for k := n - 1; k >= 0; k-- {
		if drop(k) {
			lines = append(lines[:blocks[k].start:blocks[k].start], lines[blocks[k].end+1:]...)
		}
	}
	return lines, true
}

// replaceTableBlock rewrites the k-th of blocks to hold v. Keys v still
// sets keep their lines and comments, keys it no longer sets are removed,
// and its sub-tables are replaced whole. n and ok are as for
// appendTableBlock.
func replaceTableBlock(lines []string, blocks []tableBlock, n, k int, name string, v any) (edited []string, ok bool, err error) {
	if len(blocks) != n {
		return nil, false, nil
	}
	block, err := encodeTableBlock(name, v)
	if err != nil {
		return nil, false, err
	}
	b := blocks[k]
	split := len(block)
	if i := slices.IndexFunc(block[1:], isAnyTableHeader); i >= 0 {
		split = i + 1
	}
	keys, subTables := block[1:split], block[split:]

	// Sub-tables first: they follow the keys, whose lines then stay put.
	keysEnd := sectionEnd(lines, b.start, min(nextHeader(lines, b.start)-1, b.end))
	if len(subTables) > 0 {
		subTables = append([]string{""}, subTables...)
	}
	lines = slices.Concat(lines[:keysEnd+1], subTables, lines[b.end+1:])

	ownEnd := func() int { return nextHeader(lines, b.start) - 1 }
	want := make(map[string]bool, len(keys))
	for _, line := range keys {
		if key, _, found := strings.Cut(line, " = "); found {
			want[key] = true
		}
	}
	for _, key := range tableKeys(lines, b.start+1, ownEnd()) {
		if !want[key] {
			lines = removeKeyInRange(lines, b.start+1, ownEnd(), key)
		}
	}
	for _, line := range keys {
		if key, value, found := strings.Cut(line, " = "); found {
			lines = upsertKeyInRange(lines, b.start+1, ownEnd(), key, value)
		}
	}
	return lines, true, nil
}

// tableKeys returns the keys set in lines[from..to], the lines of one
// table's own keys.
func tableKeys(lines []string, from, to int) []string {
	var keys []string
	for i := from; i <= to && i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if key, _, ok := strings.Cut(t, "="); ok {
			keys = append(keys, strings.TrimSpace(key))
		}
		i = tomlValueEnd(lines, i)
	}
	return keys
}
