// Package runtime defines the daemon-owned boundary for controlling Zellij.
//
// The package will host the internal RuntimeService interface used by agentd
// to create panes, send input, inspect runtime state, subscribe to events, and
// clean up managed resources. External transports and AI planner integration
// are intentionally deferred until the daemon core is stable.
package runtime
