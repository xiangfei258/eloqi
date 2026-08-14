package config

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// tomlTable is a minimal representation of a parsed TOML document: a map from
// section name to a map of key-value pairs (all values stored as strings).
type tomlTable map[string]map[string]string

// parseTOML reads a minimal subset of TOML from r. Supported syntax:
//   - Section headers:  [section]
//   - String values:    key = "value"
//   - Boolean values:   key = true / key = false
//   - Unquoted values:  key = value  (treated as a string)
//   - Comments:         # ... (ignored, also inside unquoted values)
//   - Blank lines are skipped.
//
// Quoted strings may contain # characters. This parser is intentionally
// minimal and is sufficient for Eloqui's configuration file; it can be
// replaced with a full TOML library in a later phase.
func parseTOML(r io.Reader) (tomlTable, error) {
	tbl := tomlTable{}
	section := "" // top-level keys (before any section header)

	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		line := stripComment(raw)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			if section == "" {
				return nil, fmt.Errorf("toml: line %d: empty section header", lineNo)
			}
			if tbl[section] == nil {
				tbl[section] = map[string]string{}
			}
			continue
		}

		eq := strings.Index(line, "=")
		if eq < 0 {
			return nil, fmt.Errorf("toml: line %d: expected key = value", lineNo)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "" {
			return nil, fmt.Errorf("toml: line %d: empty key", lineNo)
		}

		val, err := parseTOMLValue(val)
		if err != nil {
			return nil, fmt.Errorf("toml: line %d: %w", lineNo, err)
		}

		if tbl[section] == nil {
			tbl[section] = map[string]string{}
		}
		tbl[section][key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("toml: read error: %w", err)
	}
	return tbl, nil
}

// parseTOMLValue interprets a raw value string. Quoted strings have their
// quotes removed; bare true/false are kept as-is so callers can detect them.
func parseTOMLValue(s string) (string, error) {
	if len(s) >= 2 && s[0] == '"' {
		// Find the closing quote.
		end := strings.IndexByte(s[1:], '"')
		if end < 0 {
			return "", fmt.Errorf("unterminated string: %s", s)
		}
		return s[1 : 1+end], nil
	}
	return s, nil
}

// stripComment removes a trailing # comment from a line, respecting double
// quotes so that # inside a string literal is preserved.
func stripComment(line string) string {
	inQuote := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch == '"' {
			inQuote = !inQuote
		}
		if ch == '#' && !inQuote {
			return line[:i]
		}
	}
	return line
}
