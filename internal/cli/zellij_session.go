package cli

import (
	"errors"
	"os"
	"strings"
)

var ErrZellijSessionRequired = errors.New("zellij session is required: pass --zellij-session or run inside Zellij")

func ResolveZellijSession(explicit string) (string, error) {
	if session := strings.TrimSpace(explicit); session != "" {
		return session, nil
	}
	if session := strings.TrimSpace(os.Getenv("ZELLIJ_SESSION_NAME")); session != "" {
		return session, nil
	}
	return "", ErrZellijSessionRequired
}
