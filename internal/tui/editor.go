package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// ErrCancelled is returned when the user enters :cancel or :quit.
var ErrCancelled = errors.New("tui: editing cancelled")

// Editor is a dependency-injected, line-oriented terminal configuration UI.
// Keeping input, output, and persistence behind interfaces makes the complete
// edit/validate/save flow deterministic in unit tests and usable in any TTY.
type Editor struct {
	In         io.Reader
	Out        io.Writer
	Store      Store
	ReadSecret func() (string, error)
}

// NewEditor returns an editor using the supplied streams and store. Nil
// streams default to stdin/stdout when Run is called.
func NewEditor(in io.Reader, out io.Writer, store Store) *Editor {
	return &Editor{In: in, Out: out, Store: store}
}

// Run loads settings, walks through every editable field, validates the final
// value, and saves it exactly once. Blank input keeps the current value;
// optional fields accept "-" to clear them. API-key input is read with
// terminal echo disabled when stdin is an interactive terminal.
func (e *Editor) Run() (Settings, error) {
	if e == nil || e.Store == nil {
		return Settings{}, errors.New("tui: editor store is required")
	}
	in := e.In
	if in == nil {
		in = os.Stdin
	}
	out := e.Out
	if out == nil {
		out = os.Stdout
	}

	settings, err := e.Store.Load()
	if err != nil {
		return Settings{}, fmt.Errorf("tui: load configuration: %w", err)
	}
	settings = settings.Normalize()
	readSecret := e.ReadSecret
	if readSecret == nil {
		if file, ok := in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
			readSecret = func() (string, error) {
				secret, err := term.ReadPassword(int(file.Fd()))
				return string(secret), err
			}
		}
	}
	interaction := editorInteraction{scanner: bufio.NewScanner(in), out: out, readSecret: readSecret}
	if _, err := fmt.Fprintln(out, "Eloqui configuration (blank keeps current; :cancel exits)"); err != nil {
		return Settings{}, fmt.Errorf("tui: write heading: %w", err)
	}

	if settings.HotkeyModifiers, err = interaction.askString(
		"Hotkey modifiers", settings.HotkeyModifiers, true, nil,
	); err != nil {
		return Settings{}, err
	}
	if settings.HotkeyKey, err = interaction.askString(
		"Hotkey key", settings.HotkeyKey, true, nil,
	); err != nil {
		return Settings{}, err
	}
	if settings.Mode, err = interaction.askString(
		"Trigger mode (hold/toggle)", settings.Mode, false,
		func(value string) error {
			if value != "hold" && value != "toggle" {
				return errors.New("enter hold or toggle")
			}
			return nil
		},
	); err != nil {
		return Settings{}, err
	}
	if settings.ASREngine, err = interaction.askString(
		"ASR engine", settings.ASREngine, false, requiredValue,
	); err != nil {
		return Settings{}, err
	}
	if settings.ASREndpoint, err = interaction.askString(
		"ASR endpoint", settings.ASREndpoint, false, requiredValue,
	); err != nil {
		return Settings{}, err
	}
	if settings.ASRAPIKey, err = interaction.askSecret(
		"ASR API key", settings.ASRAPIKey, requiredValue,
	); err != nil {
		return Settings{}, err
	}
	if settings.ASRModel, err = interaction.askString(
		"ASR model", settings.ASRModel, false, requiredValue,
	); err != nil {
		return Settings{}, err
	}
	if settings.AutoType, err = interaction.askBool("Automatic typing", settings.AutoType); err != nil {
		return Settings{}, err
	}
	if settings.StopDelay, err = interaction.askMilliseconds("Stop delay (ms)", settings.StopDelay); err != nil {
		return Settings{}, err
	}
	if settings.Hotwords, err = interaction.askHotwords("Hotwords (comma-separated)", settings.Hotwords); err != nil {
		return Settings{}, err
	}
	if settings.Language, err = interaction.askString("Language", settings.Language, true, nil); err != nil {
		return Settings{}, err
	}

	settings = settings.Normalize()
	if err := settings.Validate(); err != nil {
		return Settings{}, err
	}
	if err := e.Store.Save(settings); err != nil {
		return Settings{}, fmt.Errorf("tui: save configuration: %w", err)
	}
	if _, err := fmt.Fprintln(out, "Configuration saved."); err != nil {
		return Settings{}, fmt.Errorf("tui: write completion message: %w", err)
	}
	return settings, nil
}

type editorInteraction struct {
	scanner    *bufio.Scanner
	out        io.Writer
	readSecret func() (string, error)
}

func (i *editorInteraction) askString(label, current string, clearable bool, validate func(string) error) (string, error) {
	for {
		value, keep, err := i.readValue(label, current)
		if err != nil {
			return "", err
		}
		if keep {
			value = current
		} else if clearable && value == "-" {
			value = ""
		}
		value = strings.TrimSpace(value)
		if validate != nil {
			if err := validate(value); err != nil {
				if err := i.invalid(err); err != nil {
					return "", err
				}
				continue
			}
		}
		return value, nil
	}
}

func (i *editorInteraction) askSecret(label, current string, validate func(string) error) (string, error) {
	for {
		display := "not set"
		if current != "" {
			display = "configured"
		}
		var value string
		var keep bool
		var err error
		if i.readSecret == nil {
			value, keep, err = i.readValue(label, display)
		} else {
			value, keep, err = i.readProtectedValue(label, display)
		}
		if err != nil {
			return "", err
		}
		if keep {
			value = current
		}
		value = strings.TrimSpace(value)
		if validate != nil {
			if err := validate(value); err != nil {
				if err := i.invalid(err); err != nil {
					return "", err
				}
				continue
			}
		}
		return value, nil
	}
}

func (i *editorInteraction) readProtectedValue(label, current string) (value string, keep bool, err error) {
	if _, err := fmt.Fprintf(i.out, "%s [%s]: ", label, current); err != nil {
		return "", false, fmt.Errorf("tui: write prompt: %w", err)
	}
	value, err = i.readSecret()
	if _, writeErr := fmt.Fprintln(i.out); writeErr != nil {
		return "", false, fmt.Errorf("tui: finish secret prompt: %w", writeErr)
	}
	if err != nil {
		return "", false, fmt.Errorf("tui: read secret input: %w", err)
	}
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, ":cancel") || strings.EqualFold(value, ":quit") {
		return "", false, ErrCancelled
	}
	return value, value == "", nil
}

func (i *editorInteraction) askBool(label string, current bool) (bool, error) {
	for {
		value, keep, err := i.readValue(label+" (yes/no)", strconv.FormatBool(current))
		if err != nil {
			return false, err
		}
		if keep {
			return current, nil
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "yes", "y", "1", "on":
			return true, nil
		case "false", "no", "n", "0", "off":
			return false, nil
		default:
			if err := i.invalid(errors.New("enter yes or no")); err != nil {
				return false, err
			}
		}
	}
}

func (i *editorInteraction) askMilliseconds(label string, current time.Duration) (time.Duration, error) {
	for {
		value, keep, err := i.readValue(label, strconv.FormatInt(current.Milliseconds(), 10))
		if err != nil {
			return 0, err
		}
		if keep {
			return current, nil
		}
		milliseconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || milliseconds < 0 {
			if err := i.invalid(errors.New("enter a non-negative whole number of milliseconds")); err != nil {
				return 0, err
			}
			continue
		}
		if milliseconds > int64((time.Duration(1<<63-1))/time.Millisecond) {
			if err := i.invalid(errors.New("delay is too large")); err != nil {
				return 0, err
			}
			continue
		}
		return time.Duration(milliseconds) * time.Millisecond, nil
	}
}

func (i *editorInteraction) askHotwords(label string, current []string) ([]string, error) {
	value, keep, err := i.readValue(label, strings.Join(current, ", "))
	if err != nil {
		return nil, err
	}
	if keep {
		return append([]string(nil), current...), nil
	}
	value = strings.TrimSpace(value)
	if value == "-" || value == "" {
		return nil, nil
	}
	return strings.Split(value, ","), nil
}

func (i *editorInteraction) readValue(label, current string) (value string, keep bool, err error) {
	_, err = fmt.Fprintf(i.out, "%s [%s]: ", label, current)
	if err != nil {
		return "", false, fmt.Errorf("tui: write prompt: %w", err)
	}
	if !i.scanner.Scan() {
		if err := i.scanner.Err(); err != nil {
			return "", false, fmt.Errorf("tui: read input: %w", err)
		}
		return "", false, io.EOF
	}
	value = strings.TrimSpace(i.scanner.Text())
	if strings.EqualFold(value, ":cancel") || strings.EqualFold(value, ":quit") {
		return "", false, ErrCancelled
	}
	return value, value == "", nil
}

func (i *editorInteraction) invalid(err error) error {
	if _, writeErr := fmt.Fprintf(i.out, "Invalid value: %v\n", err); writeErr != nil {
		return fmt.Errorf("tui: write validation error: %w", writeErr)
	}
	return nil
}

func requiredValue(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("a value is required")
	}
	return nil
}
