// Package runtime defines the daemon-owned boundary for controlling Zellij.
//
// The package exposes small service interfaces for pane control, events,
// introspection, reconciliation, cleanup, and execution plans. Consumers should
// accept the narrow interface they need instead of the full RuntimeService
// composition.
package runtime
