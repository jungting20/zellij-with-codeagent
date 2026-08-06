package codingagent

import (
	"errors"
	"fmt"
)

type AccessMode string

const (
	AccessFull     AccessMode = "full"
	AccessReadOnly AccessMode = "read-only"
)

var ErrInvalidAccessMode = errors.New("invalid coding agent access mode")

func ParseAccessMode(value string) (AccessMode, error) {
	switch AccessMode(value) {
	case "", AccessFull:
		return AccessFull, nil
	case AccessReadOnly:
		return AccessReadOnly, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidAccessMode, value)
	}
}
