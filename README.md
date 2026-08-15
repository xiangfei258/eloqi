# Eloqui

[简体中文](README.zh-CN.md) · [Installation](INSTALL.md) · [Changelog](CHANGELOG.md) · [Roadmap](TASKS.md) · [Real-device checklist](docs/REAL_DEVICE_CHECKLIST.zh-CN.md)

Eloqui is a cross-platform desktop voice-input tool written in Go. A global hotkey starts a recording, an OpenAI-compatible speech-to-text endpoint transcribes it, and Eloqui either copies the result to the clipboard or pastes it into the focused application.

> Eloqui is still under development. P2–P5 implementation and automated coverage are present, but the macOS native Objective-C/cgo bridge, all P2–P5 real-device regressions, remote CI, and tag-driven release publishing are not yet accepted. See [Verification status](#verification-status) before treating a platform as production-ready.

## Highlights

- `hold` and `toggle` hotkey modes, including modifier-only bindings.
- Explicit `idle → connecting → recording → stopping_delayed → stopping/error` state machine.
- Configurable tail-recording delay, Escape cancellation, R retry, and single-delivery protection.
- OpenAI-compatible transcription endpoint with language hints, hotwords, and optional diarization cleanup.
- Clipboard-only or automatic-paste output.
- Runtime configuration reload with validation and rollback to the last working configuration.
- Terminal configuration editor, startup `doctor`, structured logs, local usage statistics, and status overlay.
- Native platform backends for Linux, macOS, and Windows, isolated with Go build tags.

## Verification status

| Area | Current evidence | Still required |
|---|---|---|
| Linux | Full repository build/vet/race and golangci-lint passed under Linux with Xvfb; focused voice, app-reload, and X11 race repetitions also passed. | Wayland and X11 real-device regression, including microphone, hotkeys, clipboard, auto-paste, and overlay. |
| Windows | `windows/amd64` full build, vet, tests, and golangci-lint passed locally; focused `386` tests and `arm64` platform/main-program cross-build passed. | Windows real-device end-to-end and visual/permission regression. |
| macOS | Platform source and cgo-free helper logic are present; helper tests cross-compiled for amd64 and arm64. | Native Objective-C/cgo build with a macOS SDK, then Intel/Apple Silicon real-device regression. |
| CI and release | GitHub Actions CI/release workflows, actionlint, local Linux/Windows archive smoke, and documentation-link checks passed. | The current `origin` is Gitee, which does not execute `.github/workflows`; run the workflows in a GitHub repository or mirror. No remote green run or `v*` tag release has been verified. |

Automated tests do not replace desktop hardware acceptance. The exact manual steps are in [docs/REAL_DEVICE_CHECKLIST.zh-CN.md](docs/REAL_DEVICE_CHECKLIST.zh-CN.md).

## Quick start from source

Eloqui requires Go 1.25. Linux also needs X11 development headers at build time and desktop/audio commands at runtime. See [INSTALL.md](INSTALL.md) for platform-specific dependencies and permissions.

```bash
git clone <your-eloqi-repository>
cd eloqi

export GOCACHE="$PWD/.buildcache" GOMODCACHE="$PWD/.buildcache/mod"
cp eloqi.toml.example eloqi.toml
# Edit endpoint, api_key, model, and output mode before continuing.

go build -o eloqi ./cmd/eloqi
./eloqi --doctor --config ./eloqi.toml
./eloqi --config ./eloqi.toml
```

Press `Ctrl+C` to stop the daemon. Normal startup runs the same environment checks as `--doctor` and refuses to start when a required dependency is unavailable.

## CLI

```text
eloqi [--config PATH] [--debug]
eloqi --doctor [--config PATH]
eloqi --tui [--config PATH] [--debug] [--log-file PATH]
eloqi --version
```

| Option | Behavior |
|---|---|
| `--config PATH` | Use an explicit TOML configuration file. |
| `--doctor` | Print platform dependency and permission findings, then exit. |
| `--tui` | Open the terminal configuration editor, save atomically, then exit. It does not start the voice daemon. |
| `--debug` | Enable debug-level structured logs. |
| `--log-file PATH` | Override the TUI log destination. This option is intended for `--tui`. |
| `-v`, `--version` | Print the version embedded by the release build. Local builds report `dev`. |

When `--config` is omitted, Eloqui uses `ELOQUI_CONFIG` when set, otherwise an existing `~/.config/eloqi/config.toml`, and finally `./eloqi.toml`.

The terminal editor keeps the existing API key hidden, disables terminal echo while entering a new key, and preserves comments plus explicitly named extensions (`[plugin.*]`/`[x-*]` sections and `x_` fields). Other unknown sections, unknown keys, and misspelled booleans are rejected instead of being silently ignored. The editor supports these controls:

- blank input keeps the current value;
- `-` clears fields that may be empty, including hotwords;
- `:cancel` or `:quit` exits without saving.

Run the daemon and editor as separate processes when testing hot reload:

```bash
./eloqi --config ./eloqi.toml
# In another terminal:
./eloqi --tui --config ./eloqi.toml
```

The watcher observes the containing directory, debounces atomic file replacement, validates the new snapshot, and keeps or restores the previous working configuration when reload fails.

Only one daemon may run for the current user/session. A second instance exits before opening statistics or platform devices, preventing duplicate recordings, ASR requests, and text injection. The one-shot `--doctor`, `--tui`, and `--version` commands do not take the daemon lock.

## Configuration

See [eloqi.toml.example](eloqi.toml.example) for a complete annotated file.

| Key | Meaning | Default |
|---|---|---|
| `hotkey.mods` | `+`-separated modifiers: Ctrl, Alt, Super, Shift. | `Ctrl+Alt` |
| `hotkey.key` | F1–F24, Tab, CapsLock, navigation/editing keys, or Num0–Num9. Empty means a modifier-only binding. macOS supports F1–F20 because Apple does not document virtual keycodes for F21–F24. | `F1` |
| `hotkey.mode` | `hold` or `toggle`. | `hold` |
| `hotkey.stop_delay_ms` | Tail-audio delay after the stop gesture; `0` disables it. | `800` |
| `asr.engine` | ASR implementation. Only `openai-compatible` is currently supported. | `openai-compatible` |
| `asr.endpoint` | Absolute `http://` or `https://` audio-transcription URL with a host; relative URLs, other schemes, and fragments are rejected at startup. | required |
| `asr.api_key` | Bearer token. Use a non-empty placeholder only when a trusted local endpoint ignores authorization. | required |
| `asr.model` | Backend model name. | `whisper-1` |
| `asr.language` | Optional language hint accepted by the backend. | automatic |
| `asr.hotwords` | TOML string array passed as a recognition prompt after trim/deduplication; combined prompt limit is 8192 bytes. | `[]` |
| `asr.strip_diarization` | Remove complete `[timestamp][speaker]` annotations from compatible transcripts. | `false` |
| `output.auto_type` | Paste into the focused app when `true`; otherwise only update the clipboard. | `true` |

Protect the configuration file because it contains the ASR credential. On Unix-like systems, `chmod 600 eloqi.toml` is recommended.

## Runtime data and privacy

Eloqui records only successful-session counters locally: number of recordings, Unicode character counts, durations, and the latest-session values. It does not store transcript text in the statistics file or normal completion logs.

The statistics path is:

- `ELOQUI_STATE_DIR/stats.json` when `ELOQUI_STATE_DIR` is set;
- Linux: `$XDG_STATE_HOME/eloqi/stats.json`, or `~/.local/state/eloqi/stats.json`;
- macOS: `~/Library/Application Support/eloqi/stats.json`;
- Windows: `%AppData%\eloqi\stats.json`.

TUI logs use the operating system user cache directory unless `--log-file` is supplied: normally `$XDG_CACHE_HOME/eloqi/eloqi.log` (or `~/.cache/eloqi/eloqi.log`) on Linux, `~/Library/Caches/eloqi/eloqi.log` on macOS, and `%LocalAppData%\eloqi\eloqi.log` on Windows. Normal daemon logs go to stderr. Overlay failures are warnings and do not disable the voice pipeline.

## Development and release

Repository verification:

```bash
export GOCACHE="$PWD/.buildcache" GOMODCACHE="$PWD/.buildcache/mod"
unformatted="$(git ls-files -z --cached --others --exclude-standard -- '*.go' | xargs -0 gofmt -l)"
test -z "$unformatted" || { printf '%s\n' "$unformatted"; exit 1; }
go build ./...
go vet ./...
go test -race ./...
golangci-lint run
```

Linux display tests should run under Xvfb when no real X server is available: `xvfb-run -a go test -race ./...`.

The GitHub workflows are configured to:

- build, vet, and race-test on Linux, Windows, macOS Intel, and macOS Apple Silicon, plus lint on Linux;
- build Linux amd64, macOS amd64/arm64, and Windows amd64 archives for `v*` tags;
- publish `SHA256SUMS` with the GitHub Release.

Those workflows are release infrastructure, not evidence that a release exists. They remain unverified until run successfully in GitHub and followed by the real-device checklist.

## License and originality

Eloqui is original MIT-licensed code: [LICENSE](LICENSE). Binary redistributions also include [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). It was designed from functional ideas rather than copied or translated source from the GPL-licensed reference project described in the project handoff.
