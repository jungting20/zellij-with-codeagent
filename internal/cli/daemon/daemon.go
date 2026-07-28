package daemoncli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"zellij-with-codeagent/internal/cli"
	"zellij-with-codeagent/internal/registry"
	agentruntime "zellij-with-codeagent/internal/runtime"
	"zellij-with-codeagent/internal/transport"
	"zellij-with-codeagent/internal/zellij"
)

const version = "dev"

var requestDaemonShutdown = func(ctx context.Context, socketPath string, timeout time.Duration) error {
	client := transport.NewClient(transport.ClientOptions{SocketPath: socketPath, Timeout: timeout})
	_, err := client.Shutdown(ctx)
	return err
}

var stopLegacyDaemon = stopLegacyDaemonProcess

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

func Run(args []string, stdout, stderr io.Writer) int {
	return RunContext(context.Background(), args, stdout, stderr)
}

func RunContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_ = newRuntimeService()
		fmt.Fprintln(stdout, "agentd daemon skeleton")
		return 0
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "-v", "--version", "version":
		fmt.Fprintf(stdout, "agentd %s\n", version)
		return 0
	case "serve":
		socketPath, ok := parseServeArgs(args[1:], stderr)
		if !ok {
			return 2
		}
		voiceService := newDaemonVoiceService(stdout)
		defer func() {
			if err := voiceService.Close(); err != nil {
				fmt.Fprintf(stderr, "close voice service: %v\n", err)
			}
		}()
		server, err := transport.NewServer(transport.ServerOptions{
			Service:            newRuntimeService(),
			VoiceNotifications: voiceQueueAdapter{service: voiceService},
			SocketPath:         socketPath,
			Version:            version,
		})
		if err != nil {
			fmt.Fprintf(stderr, "start transport server: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "agentd serving on unix socket %s\n", socketPath)
		if err := server.ListenAndServe(ctx); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			fmt.Fprintf(stderr, "agentd serve failed: %v\n", err)
			return 1
		}
		return 0
	case "stop":
		return runStop(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown argument: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: agentd [--help] [--version] <serve|stop> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "agentd is the daemon entrypoint for the Zellij agent runtime.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  serve   Run the daemon")
	fmt.Fprintln(w, "  stop    Stop the running daemon")
}

func parseServeArgs(args []string, stderr io.Writer) (string, bool) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	if err := fs.Parse(args); err != nil {
		return "", false
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", fs.Args())
		printUsage(stderr)
		return "", false
	}
	return *socketPath, true
}

func runStop(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", cli.DefaultSocketPath, "agentd Unix socket path")
	timeout := fs.Duration("timeout", 5*time.Second, "stop timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", fs.Args())
		printUsage(stderr)
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "stop timeout must be greater than zero")
		return 2
	}
	if _, err := os.Stat(*socketPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(stdout, "agentd is not running on unix socket %s\n", *socketPath)
			return 0
		}
		fmt.Fprintf(stderr, "inspect agentd socket %s: %v\n", *socketPath, err)
		return 1
	}

	stopCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if err := requestDaemonShutdown(stopCtx, *socketPath, *timeout); err != nil {
		if !transport.IsNotFound(err) {
			fmt.Fprintf(stderr, "stop agentd via socket %s: %v\n", *socketPath, err)
			return 1
		}
		if legacyErr := stopLegacyDaemon(stopCtx, *socketPath); legacyErr != nil {
			fmt.Fprintf(stderr, "stop legacy agentd via socket %s: %v\n", *socketPath, legacyErr)
			return 1
		}
	}
	for {
		if _, err := os.Stat(*socketPath); os.IsNotExist(err) {
			fmt.Fprintf(stdout, "agentd stopped on unix socket %s\n", *socketPath)
			return 0
		}
		select {
		case <-stopCtx.Done():
			fmt.Fprintf(stderr, "wait for agentd socket %s to close: %v\n", *socketPath, stopCtx.Err())
			return 1
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func stopLegacyDaemonProcess(ctx context.Context, socketPath string) error {
	output, err := exec.CommandContext(ctx, "lsof", "-n", "-a", "-U", "-Fp", "--", socketPath).Output()
	if err != nil {
		return fmt.Errorf("find socket owner: %w", err)
	}
	pids := make(map[int]struct{})
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, "p") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimPrefix(line, "p"))
		if err != nil || pid <= 0 {
			continue
		}
		pids[pid] = struct{}{}
	}
	if len(pids) != 1 {
		return fmt.Errorf("expected one socket owner, found %d", len(pids))
	}
	var pid int
	for candidate := range pids {
		pid = candidate
	}
	commandOutput, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return fmt.Errorf("inspect socket owner %d: %w", pid, err)
	}
	if err := validateLegacyDaemonCommand(strings.TrimSpace(string(commandOutput)), socketPath); err != nil {
		return fmt.Errorf("refuse to signal socket owner %d: %w", pid, err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find socket owner process %d: %w", pid, err)
	}
	if err := process.Signal(os.Interrupt); err != nil {
		return fmt.Errorf("signal socket owner %d: %w", pid, err)
	}

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := process.Signal(syscall.Signal(0))
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove stale socket: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("check socket owner %d: %w", pid, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func validateLegacyDaemonCommand(command, socketPath string) error {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return errors.New("empty command")
	}
	args := fields[1:]
	switch filepath.Base(fields[0]) {
	case "zellij-agent":
		if len(args) < 2 || args[0] != "daemon" || args[1] != "serve" {
			return fmt.Errorf("unexpected zellij-agent command %q", command)
		}
		args = args[2:]
	case "agentd":
		if len(args) < 1 || args[0] != "serve" {
			return fmt.Errorf("unexpected agentd command %q", command)
		}
		args = args[1:]
	default:
		return fmt.Errorf("unexpected executable %q", fields[0])
	}

	configuredSocket := ""
	for i, arg := range args {
		switch {
		case arg == "--socket" && i+1 < len(args):
			configuredSocket = args[i+1]
		case strings.HasPrefix(arg, "--socket="):
			configuredSocket = strings.TrimPrefix(arg, "--socket=")
		}
	}
	if configuredSocket == "" {
		configuredSocket = cli.DefaultSocketPath
	}
	if configuredSocket != socketPath {
		return fmt.Errorf("command socket %q does not match %q", configuredSocket, socketPath)
	}
	return nil
}

func newRuntimeService() *agentruntime.Service {
	return agentruntime.NewService(agentruntime.Options{
		Registry:           registry.New(),
		Backend:            zellij.NewBackend(zellij.Options{}),
		SubscriptionRunner: agentruntime.ExecSubscriptionRunner{},
	})
}
