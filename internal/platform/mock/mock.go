// Package mock provides deterministic, in-memory implementations of the
// platform capability interfaces. They are intended for unit tests of the
// engine and plugins, where no real audio device, hotkey provider or network
// backend is available.
//
// Each mock records its lifecycle and inputs so tests can assert on them, and
// exposes optional error fields to simulate failures.
package mock
