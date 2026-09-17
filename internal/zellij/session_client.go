package zellij

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/creack/pty"
)

type sessionClientStarter interface {
	startSessionClient(context.Context, string, string) (func(), error)
}

// Detached Zellij sessions can lose their client dimensions. Keep a sized client
// connected until the new tab has a terminal, then release only our own client.
func (b *CLIBackend) prepareTabClient(ctx context.Context, session string) (func(), bool, error) {
	starter, ok := b.runner.(sessionClientStarter)
	if !ok {
		return func() {}, false, nil
	}
	connected := func() (bool, error) {
		result, err := b.run(ctx, "list clients", newActionCommand(b.binary, session, "list-clients"))
		return len(strings.Split(strings.TrimSpace(result.Stdout), "\n")) > 1, err
	}
	if found, err := connected(); err != nil || found {
		return func() {}, false, err
	}
	stop, err := starter.startSessionClient(ctx, b.binary, session)
	if err != nil {
		return func() {}, false, err
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if found, err := connected(); err != nil {
			stop()
			return func() {}, false, err
		} else if found {
			return stop, true, nil
		}
		select {
		case <-ctx.Done():
			stop()
			return func() {}, false, ctx.Err()
		case <-timer.C:
			stop()
			return func() {}, false, fmt.Errorf("temporary Zellij client did not connect to %s", session)
		case <-ticker.C:
		}
	}
}

func (ExecRunner) startSessionClient(ctx context.Context, binary, session string) (func(), error) {
	cmd := exec.CommandContext(ctx, binary, "attach", session)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch key {
		case "ZELLIJ", "ZELLIJ_SESSION_NAME", "ZELLIJ_PANE_ID", "TERM":
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		return nil, fmt.Errorf("start temporary Zellij client: %w", err)
	}
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, terminal)
		close(readDone)
	}()
	return func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = terminal.Close()
		<-readDone
	}, nil
}
