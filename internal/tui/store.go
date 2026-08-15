package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xiangchang24/eloqi/internal/config"
)

// Store is the persistence boundary used by Editor.
type Store interface {
	Load() (Settings, error)
	Save(Settings) error
}

// FileStore loads and atomically saves an Eloqui TOML file.
type FileStore struct {
	Path string
}

// Load reads one coherent configuration snapshot. The core config owns every
// field exposed by Settings, so no second pass can accidentally mix versions
// when an editor atomically replaces the file.
func (s FileStore) Load() (Settings, error) {
	if strings.TrimSpace(s.Path) == "" {
		return Settings{}, errors.New("tui: configuration path is empty")
	}
	cfg, err := config.Load(s.Path)
	if err != nil {
		return Settings{}, err
	}
	return FromConfig(cfg), nil
}

// Save validates settings and replaces the target atomically from a temporary
// file in the same directory. Known values are merged into the existing TOML
// so comments, surrounding layout, plugin.* sections, and x_ extension fields
// are preserved.
func (s FileStore) Save(settings Settings) error {
	if strings.TrimSpace(s.Path) == "" {
		return errors.New("tui: configuration path is empty")
	}
	settings = settings.Normalize()
	if err := settings.Validate(); err != nil {
		return err
	}

	absPath, err := filepath.Abs(s.Path)
	if err != nil {
		return fmt.Errorf("tui: resolve configuration path: %w", err)
	}
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("tui: create configuration directory: %w", err)
	}
	existing, err := os.ReadFile(absPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("tui: read existing configuration: %w", err)
	}
	contents := mergeSettings(existing, settings)

	tmp, err := os.CreateTemp(dir, ".eloqi-config-*.tmp")
	if err != nil {
		return fmt.Errorf("tui: create temporary configuration: %w", err)
	}
	tmpPath := tmp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("tui: secure temporary configuration: %w", err)
	}
	if _, err := tmp.Write(contents); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("tui: write temporary configuration: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("tui: sync temporary configuration: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("tui: close temporary configuration: %w", err)
	}
	if err := os.Rename(tmpPath, absPath); err != nil {
		return fmt.Errorf("tui: replace configuration: %w", err)
	}
	keepTemp = false
	return nil
}

type tomlSetting struct {
	key   string
	value string
}

type tomlSettingsSection struct {
	name     string
	settings []tomlSetting
}

func desiredSettings(settings Settings) []tomlSettingsSection {
	return []tomlSettingsSection{
		{
			name: "hotkey",
			settings: []tomlSetting{
				{key: "mods", value: strconv.Quote(settings.HotkeyModifiers)},
				{key: "key", value: strconv.Quote(settings.HotkeyKey)},
				{key: "mode", value: strconv.Quote(settings.Mode)},
				{key: "stop_delay_ms", value: strconv.FormatInt(settings.StopDelay.Milliseconds(), 10)},
			},
		},
		{
			name: "asr",
			settings: []tomlSetting{
				{key: "engine", value: strconv.Quote(settings.ASREngine)},
				{key: "endpoint", value: strconv.Quote(settings.ASREndpoint)},
				{key: "api_key", value: strconv.Quote(settings.ASRAPIKey)},
				{key: "model", value: strconv.Quote(settings.ASRModel)},
				{key: "language", value: strconv.Quote(settings.Language)},
				{key: "hotwords", value: renderStringArray(settings.Hotwords)},
				{key: "strip_diarization", value: strconv.FormatBool(settings.StripDiarization)},
			},
		},
		{
			name: "output",
			settings: []tomlSetting{
				{key: "auto_type", value: strconv.FormatBool(settings.AutoType)},
			},
		},
	}
}

func mergeSettings(existing []byte, settings Settings) []byte {
	sections := desiredSettings(settings)
	desired := make(map[string]map[string]string, len(sections))
	order := make(map[string][]string, len(sections))
	for _, section := range sections {
		desired[section.name] = make(map[string]string, len(section.settings))
		for _, setting := range section.settings {
			desired[section.name][setting.key] = setting.value
			order[section.name] = append(order[section.name], setting.key)
		}
	}

	if len(existing) == 0 {
		return renderNewSettings(sections)
	}
	lineEnding := "\n"
	if strings.Contains(string(existing), "\r\n") {
		lineEnding = "\r\n"
	}
	normalized := strings.ReplaceAll(string(existing), "\r\n", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")
	lines := strings.Split(normalized, "\n")
	result := make([]string, 0, len(lines)+16)
	seenSections := make(map[string]bool, len(sections))
	seenKeys := make(map[string]map[string]bool, len(sections))
	current := ""

	appendMissing := func(section string) {
		if desired[section] == nil {
			return
		}
		if seenKeys[section] == nil {
			seenKeys[section] = make(map[string]bool)
		}
		for _, key := range order[section] {
			if seenKeys[section][key] {
				continue
			}
			result = append(result, key+" = "+desired[section][key])
			seenKeys[section][key] = true
		}
	}

	for _, line := range lines {
		code, comment := splitTOMLComment(line)
		trimmed := strings.TrimSpace(strings.TrimPrefix(code, "\ufeff"))
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			appendMissing(current)
			current = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			seenSections[current] = true
			result = append(result, line)
			continue
		}

		equals := strings.IndexByte(code, '=')
		if equals >= 0 && desired[current] != nil {
			key := strings.TrimSpace(code[:equals])
			if value, known := desired[current][key]; known {
				if seenKeys[current] == nil {
					seenKeys[current] = make(map[string]bool)
				}
				seenKeys[current][key] = true
				indent := code[:len(code)-len(strings.TrimLeft(code, " \t"))]
				replacement := indent + key + " = " + value
				if comment != "" {
					replacement += " " + strings.TrimSpace(comment)
				}
				result = append(result, replacement)
				continue
			}
		}
		result = append(result, line)
	}
	appendMissing(current)

	for _, section := range sections {
		if seenSections[section.name] {
			continue
		}
		if len(result) > 0 && strings.TrimSpace(result[len(result)-1]) != "" {
			result = append(result, "")
		}
		result = append(result, "["+section.name+"]")
		for _, setting := range section.settings {
			result = append(result, setting.key+" = "+setting.value)
		}
	}
	return []byte(strings.Join(result, lineEnding) + lineEnding)
}

func renderNewSettings(sections []tomlSettingsSection) []byte {
	lines := []string{"# Eloqui configuration. Saved by the terminal editor.", ""}
	for index, section := range sections {
		lines = append(lines, "["+section.name+"]")
		for _, setting := range section.settings {
			lines = append(lines, setting.key+" = "+setting.value)
		}
		if index != len(sections)-1 {
			lines = append(lines, "")
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func renderStringArray(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = strconv.Quote(value)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func splitTOMLComment(line string) (string, string) {
	inQuote := false
	escaped := false
	for index := 0; index < len(line); index++ {
		char := line[index]
		if escaped {
			escaped = false
			continue
		}
		if inQuote && char == '\\' {
			escaped = true
			continue
		}
		if char == '"' {
			inQuote = !inQuote
			continue
		}
		if char == '#' && !inQuote {
			return line[:index], line[index:]
		}
	}
	return line, ""
}
