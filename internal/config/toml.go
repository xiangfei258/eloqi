package config

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
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
			if _, exists := tbl[section]; exists {
				return nil, fmt.Errorf("toml: line %d: duplicate section %q", lineNo, section)
			}
			tbl[section] = map[string]string{}
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
		if _, exists := tbl[section][key]; exists {
			return nil, fmt.Errorf("toml: line %d: duplicate key %q", lineNo, key)
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
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `"`) {
		end, err := closingDoubleQuote(s)
		if err != nil {
			return "", err
		}
		if trailing := strings.TrimSpace(s[end+1:]); trailing != "" {
			return "", fmt.Errorf("unexpected text after string")
		}
		value, err := strconv.Unquote(s[:end+1])
		if err != nil {
			return "", fmt.Errorf("invalid string: %w", err)
		}
		return value, nil
	}
	return s, nil
}

// parseTOMLStringArray decodes the subset of TOML arrays used by hotwords.
// It accepts comma-separated double-quoted strings, including escaped quotes,
// backslashes and a trailing comma.
func parseTOMLStringArray(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, fmt.Errorf("expected an array of strings")
	}
	rest := strings.TrimSpace(s[1 : len(s)-1])
	if rest == "" {
		return []string{}, nil
	}

	values := make([]string, 0, 4)
	for rest != "" {
		if rest[0] != '"' {
			return nil, fmt.Errorf("array values must be double-quoted strings")
		}
		end, err := closingDoubleQuote(rest)
		if err != nil {
			return nil, err
		}
		value, err := strconv.Unquote(rest[:end+1])
		if err != nil {
			return nil, fmt.Errorf("invalid array string: %w", err)
		}
		values = append(values, value)
		rest = strings.TrimSpace(rest[end+1:])
		if rest == "" {
			break
		}
		if rest[0] != ',' {
			return nil, fmt.Errorf("expected comma between array values")
		}
		rest = strings.TrimSpace(rest[1:])
	}
	return values, nil
}

func closingDoubleQuote(s string) (int, error) {
	escaped := false
	for i := 1; i < len(s); i++ {
		switch {
		case escaped:
			escaped = false
		case s[i] == '\\':
			escaped = true
		case s[i] == '"':
			return i, nil
		}
	}
	// Values may contain API keys. Never echo malformed input into errors,
	// startup stderr, structured logs, or the status overlay.
	return 0, fmt.Errorf("unterminated string")
}

// stripComment removes a trailing # comment from a line, respecting double
// quotes so that # inside a string literal is preserved.
func stripComment(line string) string {
	inQuote := false
	escaped := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if inQuote && escaped {
			escaped = false
			continue
		}
		if inQuote && ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			inQuote = !inQuote
		}
		if ch == '#' && !inQuote {
			return line[:i]
		}
	}
	return line
}
