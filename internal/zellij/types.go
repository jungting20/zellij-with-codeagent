package zellij

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const defaultBinary = "zellij"

var (
	ErrEmptyPaneID = errors.New("zellij returned an empty pane id")
	ErrEmptyTabID  = errors.New("zellij returned an empty tab id")
	ErrMissingPane = errors.New("zellij pane id is required")
	ErrMissingTab  = errors.New("zellij tab id is required")
)

type PaneID string
type TabID int

type Backend interface {
	CreateTab(ctx context.Context, req CreateTabRequest) (TabID, error)
	CloseTab(ctx context.Context, req CloseTabRequest) error
	CreatePane(ctx context.Context, req CreatePaneRequest) (PaneID, error)
	ClosePane(ctx context.Context, req ClosePaneRequest) error
	SendInput(ctx context.Context, req SendInputRequest) error
	ListPanes(ctx context.Context) ([]Pane, error)
	DumpScreen(ctx context.Context, req DumpScreenRequest) (string, error)
	SubscribeCommand(req SubscribeRequest) (CommandSpec, error)
}

type Options struct {
	Binary  string
	Session string
	Runner  CommandRunner
}

type CreatePaneRequest struct {
	Name     string
	CWD      string
	TabID    *TabID
	Floating bool
	Command  []string
}

type CreateTabRequest struct {
	Name    string
	CWD     string
	Command []string
}

type CloseTabRequest struct {
	TabID *TabID
}

type ClosePaneRequest struct {
	PaneID PaneID
}

type SendInputRequest struct {
	PaneID PaneID
	Text   string
}

type DumpScreenRequest struct {
	PaneID PaneID
	Full   bool
	ANSI   bool
}

type SubscribeRequest struct {
	PaneID PaneID
	JSON   bool
	ANSI   bool
}

type Pane struct {
	ID         PaneID
	IsPlugin   bool
	IsFocused  bool
	IsFloating bool
	Title      string
	Command    string
	CWD        string
	Exited     bool
	ExitStatus *int
	TabID      int
	TabName    string
	Rows       int
	Columns    int
	X          int
	Y          int
}

func (p *Pane) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID         json.RawMessage `json:"id"`
		IsPlugin   bool            `json:"is_plugin"`
		IsFocused  bool            `json:"is_focused"`
		IsFloating bool            `json:"is_floating"`
		Title      string          `json:"title"`
		Command    string          `json:"pane_command"`
		CWD        string          `json:"pane_cwd"`
		Exited     bool            `json:"exited"`
		ExitStatus *int            `json:"exit_status"`
		TabID      int             `json:"tab_id"`
		TabName    string          `json:"tab_name"`
		Rows       int             `json:"pane_rows"`
		Columns    int             `json:"pane_columns"`
		X          int             `json:"pane_x"`
		Y          int             `json:"pane_y"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	id, err := parsePaneID(raw.ID)
	if err != nil {
		return err
	}

	*p = Pane{
		ID:         id,
		IsPlugin:   raw.IsPlugin,
		IsFocused:  raw.IsFocused,
		IsFloating: raw.IsFloating,
		Title:      raw.Title,
		Command:    raw.Command,
		CWD:        raw.CWD,
		Exited:     raw.Exited,
		ExitStatus: raw.ExitStatus,
		TabID:      raw.TabID,
		TabName:    raw.TabName,
		Rows:       raw.Rows,
		Columns:    raw.Columns,
		X:          raw.X,
		Y:          raw.Y,
	}
	return nil
}

type CommandSpec struct {
	Name string
	Args []string
}

type CommandResult struct {
	Stdout string
	Stderr string
}

type CommandRunner interface {
	Run(ctx context.Context, spec CommandSpec) (CommandResult, error)
}

type CommandError struct {
	Operation string
	Spec      CommandSpec
	Stderr    string
	Err       error
}

func (e *CommandError) Error() string {
	detail := strings.TrimSpace(e.Stderr)
	if detail == "" && e.Err != nil {
		detail = e.Err.Error()
	}
	if detail == "" {
		detail = "unknown error"
	}

	return fmt.Sprintf("zellij %s failed: %s", e.Operation, detail)
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

func parsePaneID(data json.RawMessage) (PaneID, error) {
	if len(data) == 0 || string(data) == "null" {
		return "", ErrEmptyPaneID
	}

	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		return cleanPaneID(text)
	}

	var number int
	if err := json.Unmarshal(data, &number); err == nil {
		return PaneID("terminal_" + strconv.Itoa(number)), nil
	}

	return "", fmt.Errorf("parse zellij pane id: %s", string(data))
}

func cleanPaneID(value string) (PaneID, error) {
	id := strings.TrimSpace(value)
	if id == "" {
		return "", ErrEmptyPaneID
	}
	return PaneID(id), nil
}

func parseTabID(value string) (TabID, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return 0, ErrEmptyTabID
	}

	id, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("parse zellij tab id: %w", err)
	}
	return TabID(id), nil
}
