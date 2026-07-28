package ticketworker

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"unicode/utf16"
)

var errVoiceNotifierClosed = errors.New("voice notifier is closed")

type VoiceNotifier interface {
	Notify(string) error
	Close() error
}

type speechBackend struct {
	path string
	args func(string) []string
}

func (b speechBackend) speak(ctx context.Context, message string) error {
	err := exec.CommandContext(ctx, b.path, b.args(message)...).Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func resolveSpeechBackend(goos string, lookPath func(string) (string, error)) (speechBackend, error) {
	type candidate struct {
		name string
		args func(string) []string
	}

	messageOnly := func(message string) []string { return []string{message} }
	spdSayArgs := func(message string) []string { return []string{"--wait", message} }
	windowsArgs := func(message string) []string {
		encodedMessage := base64.StdEncoding.EncodeToString([]byte(message))
		script := fmt.Sprintf("$message = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')); Add-Type -AssemblyName System.Speech; $speaker = New-Object System.Speech.Synthesis.SpeechSynthesizer; $speaker.Speak($message)", encodedMessage)
		return []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShellCommand(script)}
	}

	var candidates []candidate
	switch goos {
	case "darwin":
		candidates = []candidate{{name: "say", args: messageOnly}}
	case "linux":
		candidates = []candidate{
			{name: "spd-say", args: spdSayArgs},
			{name: "espeak", args: messageOnly},
		}
	case "windows":
		candidates = []candidate{
			{name: "powershell.exe", args: windowsArgs},
			{name: "pwsh.exe", args: windowsArgs},
		}
	}

	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.name)
		path, err := lookPath(candidate.name)
		if err == nil {
			return speechBackend{path: path, args: candidate.args}, nil
		}
	}
	if len(names) == 0 {
		return speechBackend{}, fmt.Errorf("resolve speech backend for OS %q: unsupported OS (candidates: none)", goos)
	}
	return speechBackend{}, fmt.Errorf("resolve speech backend for OS %q: no executable found (candidates: %s)", goos, strings.Join(names, ", "))
}

func encodePowerShellCommand(command string) string {
	units := utf16.Encode([]rune(command))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func NewNativeVoiceNotifier(log io.Writer) VoiceNotifier {
	backend, err := resolveSpeechBackend(runtime.GOOS, exec.LookPath)
	if err != nil {
		return unavailableVoiceNotifier{err: err}
	}
	return newSerialVoiceNotifier(backend.speak, log)
}

type unavailableVoiceNotifier struct {
	err error
}

func (n unavailableVoiceNotifier) Notify(string) error {
	return n.err
}

func (unavailableVoiceNotifier) Close() error {
	return nil
}

type serialVoiceNotifier struct {
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []string
	closed bool

	speak  func(context.Context, string) error
	log    io.Writer
	cancel context.CancelFunc
	done   chan struct{}
	ctx    context.Context
}

func newSerialVoiceNotifier(speak func(context.Context, string) error, log io.Writer) VoiceNotifier {
	if log == nil {
		log = io.Discard
	}
	ctx, cancel := context.WithCancel(context.Background())
	notifier := &serialVoiceNotifier{
		speak:  speak,
		log:    log,
		cancel: cancel,
		done:   make(chan struct{}),
		ctx:    ctx,
	}
	notifier.cond = sync.NewCond(&notifier.mu)
	go notifier.run()
	return notifier
}

func (n *serialVoiceNotifier) Notify(message string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return errVoiceNotifierClosed
	}
	n.queue = append(n.queue, message)
	n.cond.Signal()
	return nil
}

func (n *serialVoiceNotifier) Close() error {
	n.mu.Lock()
	if !n.closed {
		n.closed = true
		clear(n.queue)
		n.queue = nil
		n.cancel()
		n.cond.Broadcast()
	}
	done := n.done
	n.mu.Unlock()

	<-done
	return nil
}

func (n *serialVoiceNotifier) run() {
	defer close(n.done)
	for {
		n.mu.Lock()
		for len(n.queue) == 0 && !n.closed {
			n.cond.Wait()
		}
		if n.closed {
			n.mu.Unlock()
			return
		}
		message := n.queue[0]
		n.queue[0] = ""
		n.queue = n.queue[1:]
		n.mu.Unlock()

		if err := n.speak(n.ctx, message); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(n.log, "voice notification failed: %v\n", err)
		}
	}
}
