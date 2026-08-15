# Changelog

All notable changes to Eloqui are recorded here. A verified `v0.1.0-rc.2` prerelease exists, but no stable release has been accepted yet.

This file follows the structure of [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Documentation

- Recorded the partial Windows 11 real-device acceptance of the downloaded `v0.1.0-rc.2` package. Physical hotkey/microphone capture, zero-delay and long-hold behavior, exact Chinese/emoji/newline clipboard output plus one automatic-paste run, recording cancellation with Escape, fail-once R retry, overlay interaction, statistics, shutdown, and restart passed through a loopback WAV-validating endpoint; real-model, ordinary-user, remaining cancellation/error/TUI, high-DPI/multi-display, and second-application microphone checks remain open.

## [0.1.0-rc.2] - 2026-08-15

### Changed

- Release build jobs now execute each native Linux, Windows, macOS Intel, and macOS Apple Silicon binary and require its `--version` output to match the tag before archiving.

### Verification boundary

- Release-source commit `2a73fec` passed GitHub CI run [`31868625541`](https://github.com/xiangfei258/eloqi/actions/runs/31868625541) on Linux, Windows, macOS Intel, and macOS Apple Silicon, plus Linux lint.
- The [`v0.1.0-rc.2` Release run](https://github.com/xiangfei258/eloqi/actions/runs/31868700354) passed all gates and native version assertions; its four archives and `SHA256SUMS` passed independent download, digest, layout, required-asset, and documentation-link verification.
- Stable-release acceptance remains blocked on the per-platform minimum startup and real-device checklist.

## [0.1.0-rc.1] - 2026-08-15

> Superseded by `v0.1.0-rc.2`, which adds native packaged-binary version assertions.

### Added

- P2 explicit voice-session state machine covering idle, connection, recording, delayed stop, finalization, and error states.
- Hold/toggle interaction, modifier-only hotkeys, configurable tail delay including explicit zero, Escape cancellation, R retry with fresh recorder/ASR dependencies, and error timeout.
- Session-level atomic output claim so final callbacks and `Finalize` cannot emit the same text twice.
- Ordered non-blocking state/session hooks for overlay and statistics integration.
- Native Windows backends for global hotkeys, WinMM recording, Unicode clipboard, SendInput paste, and a non-activating Win32 status capsule.
- Native macOS source backends for CGEventTap hotkeys, AudioQueue recording, NSPasteboard, Command+V injection, and an NSPanel helper.
- Terminal configuration editor for hotkeys, ASR settings/secret, output mode, stop delay, hotwords, and language.
- Directory-level configuration watcher with polling, content hashing, debounce, validation, bounded asynchronous callbacks, and safe shutdown.
- Runtime hot reload with per-generation hotkey providers and rollback through a fresh provider when applying a new configuration fails.
- `--doctor`, `--tui`, `--debug`, `--log-file`, and release-injected version support.
- Platform-aware doctor checks with actionable Linux dependency/evdev guidance and macOS/Windows permission guidance.
- Structured JSON logging; TUI sessions write only to a private log file so prompts are not polluted.
- Persistent local statistics for successful recording count, Unicode character counts, total/last durations, and last-session values.
- ASR hotword prompt support using a TOML string array.
- Asynchronous overlay controller plus Linux X11/notification, macOS NSPanel, and Windows Win32 backends.
- Three-OS GitHub Actions CI definition, golangci-lint configuration, tag packaging workflow, release archives, and SHA-256 checksum generation.
- Per-user/session single-instance enforcement to prevent duplicate recording, ASR billing, statistics writes, and text injection.
- Strict configuration keys and booleans, startup-time hotword prompt limits, bounded desktop commands, and sanitized bounded backend errors.
- English and Simplified Chinese README files, installation guide, annotated configuration example, and real-device regression checklist.

### Changed

- The voice lifecycle now keeps hotkey processing independent from recorder stop, final audio send, ASR finalization, output, statistics, and overlay callbacks.
- Configuration parsing now supports escaped TOML strings, inline comments, string arrays, engine selection, hotwords, and an explicit zero stop delay.
- The TUI saves through a same-directory temporary file and atomic rename while preserving comments and explicitly named `plugin.*`/`x-*` sections and `x_` fields.
- Successful-session logs contain counts and duration rather than transcript text.
- Linux overlay selection is runtime-aware: native X11 capsule under X11 and `notify-send` fallback under Wayland.

### Fixed

- Duplicate final output when an ASR final callback races the explicit finalization return.
- Callback backpressure that could delay voice-state delivery or watcher shutdown.
- Invalid hot reload replacing a working runtime configuration.
- API-key echo in an interactive terminal editor.
- Doctor incorrectly applying Wayland evdev warnings to X11 sessions.
- Statistics partial updates on persistence failure.
- Stale hotkey edges crossing a runtime reload and unexpectedly starting a new recording.
- Windows WinMM cleanup releasing native-owned headers, sample buffers, events, or pins before ownership was returned; uncertain or stuck pump ownership is now quarantined and shutdown remains bounded.
- Endpoint, malformed TOML, and backend transport errors exposing sensitive URL or configuration fragments.

### Verification boundary

- Linux full-repository build/vet/race tests passed under Xvfb; focused voice and X11 race repetitions also passed.
- Windows amd64 full build/vet/test passed; focused 386 tests and an arm64 platform-package cross-build passed.
- Final Linux and Windows local golangci-lint runs reported `0 issues`; actionlint and repository Markdown-link checks passed.
- Local Linux and Windows release-archive smoke tests passed version injection, required-asset layout, documentation-link, and SHA-256 verification.
- The macOS Objective-C/cgo bridge passed native build, vet, and race tests on GitHub-hosted Intel and Apple Silicon macOS runners.
- Linux Wayland has partial P2–P5 real-device evidence; its remaining fine-grained hotkey/auto-paste/visual checks and all Linux X11, macOS, and Windows real-device regressions remain pending.
- The first [`v0.1.0-rc.1` Release run](https://github.com/xiangfei258/eloqi/actions/runs/31867426858) validated the gated four-platform packaging and checksum pipeline; `v0.1.0-rc.2` supersedes it for packaged-binary version execution coverage.
