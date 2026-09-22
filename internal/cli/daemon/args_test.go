package daemoncli

import (
	"bytes"
	"testing"
)

// Check the default at the parser boundary without locking the user's live
// /tmp/agentd.sock. Command startup is covered with an isolated socket.
func TestParseServeArgsDefaultsSocket(t *testing.T) {
	var stderr bytes.Buffer
	socket, db, ok := parseServeArgs(nil, &stderr)
	if !ok || socket != "/tmp/agentd.sock" || db != "" || stderr.Len() != 0 {
		t.Fatalf("parseServeArgs() = %q, %q, %t; stderr=%q", socket, db, ok, stderr.String())
	}
}
