// Package platform defines the capability interfaces that decouple Eloqui's
// core logic from any particular operating system or backend.
//
// Each capability (global hotkeys, audio capture, speech recognition,
// clipboard access and key injection) is described by a small interface.
// Concrete implementations live behind build tags for each platform, while
// the mock package provides deterministic in-memory implementations for
// tests.
//
// The contracts here are deliberately narrow. Higher layers (the engine and
// plugins) depend only on these interfaces, never on a concrete platform.
package platform
