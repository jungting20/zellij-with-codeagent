package ticketworker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
)

func TestResolveSpeechBackend(t *testing.T) {
	tests := []struct {
		name        string
		goos        string
		executables map[string]string
		wantPath    string
		wantArgs    []string
	}{
		{
			name:        "macOS uses say",
			goos:        "darwin",
			executables: map[string]string{"say": "/usr/bin/say"},
			wantPath:    "/usr/bin/say",
			wantArgs:    []string{"--", "hello"},
		},
		{
			name: "Linux prefers spd-say",
			goos: "linux",
			executables: map[string]string{
				"spd-say": "/usr/bin/spd-say",
				"espeak":  "/usr/bin/espeak",
			},
			wantPath: "/usr/bin/spd-say",
			wantArgs: []string{"--wait", "--", "hello"},
		},
		{
			name:        "Linux falls back to espeak",
			goos:        "linux",
			executables: map[string]string{"espeak": "/usr/bin/espeak"},
			wantPath:    "/usr/bin/espeak",
			wantArgs:    []string{"--", "hello"},
		},
		{
			name: "Windows prefers Windows PowerShell",
			goos: "windows",
			executables: map[string]string{
				"powershell.exe": `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
				"pwsh.exe":       `C:\Program Files\PowerShell\7\pwsh.exe`,
			},
			wantPath: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
			wantArgs: nil,
		},
		{
			name:        "Windows falls back to PowerShell Core",
			goos:        "windows",
			executables: map[string]string{"pwsh.exe": `C:\Program Files\PowerShell\7\pwsh.exe`},
			wantPath:    `C:\Program Files\PowerShell\7\pwsh.exe`,
			wantArgs:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := resolveSpeechBackend(tt.goos, mapLookPath(tt.executables))
			if err != nil {
				t.Fatal(err)
			}
			if backend.path != tt.wantPath {
				t.Fatalf("backend path = %q, want %q", backend.path, tt.wantPath)
			}
			if got := backend.args("hello"); tt.wantArgs != nil && !reflect.DeepEqual(got, tt.wantArgs) {
				t.Fatalf("backend args = %q, want %q", got, tt.wantArgs)
			}
		})
	}
}

func TestResolveSpeechBackendTerminatesUnixOptionParsing(t *testing.T) {
	const message = "--voice=attacker-controlled"
	tests := []struct {
		name        string
		goos        string
		executables map[string]string
		wantArgs    []string
	}{
		{
			name:        "macOS say",
			goos:        "darwin",
			executables: map[string]string{"say": "/usr/bin/say"},
			wantArgs:    []string{"--", message},
		},
		{
			name:        "Linux spd-say retains wait",
			goos:        "linux",
			executables: map[string]string{"spd-say": "/usr/bin/spd-say"},
			wantArgs:    []string{"--wait", "--", message},
		},
		{
			name:        "Linux espeak",
			goos:        "linux",
			executables: map[string]string{"espeak": "/usr/bin/espeak"},
			wantArgs:    []string{"--", message},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := resolveSpeechBackend(tt.goos, mapLookPath(tt.executables))
			if err != nil {
				t.Fatal(err)
			}
			if got := backend.args(message); !reflect.DeepEqual(got, tt.wantArgs) {
				t.Fatalf("backend args = %q, want %q", got, tt.wantArgs)
			}
		})
	}
}

func TestSpeechBackendWindowsEncodedCommandPreservesMessage(t *testing.T) {
	message := "hello'; Write-Error 'owned'\n$([char]0x41) 안녕 🔊"
	tests := []struct {
		name        string
		executables map[string]string
	}{
		{
			name:        "Windows PowerShell",
			executables: map[string]string{"powershell.exe": `C:\Windows\powershell.exe`},
		},
		{
			name:        "PowerShell Core",
			executables: map[string]string{"pwsh.exe": `C:\Program Files\PowerShell\7\pwsh.exe`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := resolveSpeechBackend("windows", mapLookPath(tt.executables))
			if err != nil {
				t.Fatal(err)
			}
			args := backend.args(message)
			if len(args) != 4 {
				t.Fatalf("args = %q, want three fixed flags and one encoded command", args)
			}
			if want := []string{"-NoProfile", "-NonInteractive", "-EncodedCommand"}; !reflect.DeepEqual(args[:3], want) {
				t.Fatalf("args prefix = %q, want %q", args[:3], want)
			}
			for _, arg := range args {
				if strings.Contains(arg, message) {
					t.Fatalf("raw message appears in process argument %q", arg)
				}
			}

			command := decodeUTF16LEBase64(t, args[3])
			if strings.Contains(command, message) {
				t.Fatalf("raw message appears in decoded PowerShell command %q", command)
			}
			const messagePrefix = "$message = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('"
			rest, ok := strings.CutPrefix(command, messagePrefix)
			if !ok {
				t.Fatalf("decoded command %q does not start with safe message decoding", command)
			}
			encodedMessage, rest, ok := strings.Cut(rest, "')); ")
			if !ok {
				t.Fatalf("decoded command %q does not terminate encoded message", command)
			}
			messageBytes, err := base64.StdEncoding.DecodeString(encodedMessage)
			if err != nil {
				t.Fatalf("decode embedded message: %v", err)
			}
			if got := string(messageBytes); got != message {
				t.Fatalf("decoded message = %q, want %q", got, message)
			}
			if strings.Contains(rest, "$args[0]") || !strings.Contains(rest, "$speaker.Speak($message)") {
				t.Fatalf("decoded command uses unsafe speech invocation: %q", command)
			}
		})
	}
}

func decodeUTF16LEBase64(t *testing.T, value string) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode PowerShell command: %v", err)
	}
	if len(data)%2 != 0 {
		t.Fatalf("encoded PowerShell command has odd byte length %d", len(data))
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	return string(utf16.Decode(units))
}

func TestNormalizeSpeechError(t *testing.T) {
	processErr := errors.New("speech process failed")
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "canceled context overrides process error", ctx: canceledCtx, want: context.Canceled},
		{name: "live context preserves process error", ctx: context.Background(), want: processErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeSpeechError(tt.ctx, processErr); got != tt.want {
				t.Fatalf("normalizeSpeechError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSerialVoiceNotifierCancellationIsNotLogged(t *testing.T) {
	var log bytes.Buffer
	started := make(chan struct{})
	notifier := newSerialVoiceNotifier(func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, &log)

	if err := notifier.Notify("cancel me"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for speaker to start")
	}
	if err := notifier.Close(); err != nil {
		t.Fatal(err)
	}
	if got := log.String(); got != "" {
		t.Fatalf("cancellation log = %q, want empty", got)
	}
}

func TestResolveSpeechBackendReportsUnavailableCandidates(t *testing.T) {
	tests := []struct {
		name           string
		goos           string
		wantCandidates []string
	}{
		{name: "macOS", goos: "darwin", wantCandidates: []string{"say"}},
		{name: "Linux", goos: "linux", wantCandidates: []string{"spd-say", "espeak"}},
		{name: "Windows", goos: "windows", wantCandidates: []string{"powershell.exe", "pwsh.exe"}},
		{name: "unsupported OS", goos: "plan9", wantCandidates: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveSpeechBackend(tt.goos, mapLookPath(nil))
			if err == nil {
				t.Fatal("resolveSpeechBackend() error = nil, want unavailable error")
			}
			if !strings.Contains(err.Error(), tt.goos) {
				t.Fatalf("error %q does not list OS %q", err, tt.goos)
			}
			for _, candidate := range tt.wantCandidates {
				if !strings.Contains(err.Error(), candidate) {
					t.Fatalf("error %q does not list candidate %q", err, candidate)
				}
			}
		})
	}
}

func mapLookPath(executables map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path := executables[name]; path != "" {
			return path, nil
		}
		return "", errors.New("executable not found")
	}
}

func TestSerialVoiceNotifierPreservesFIFOOrder(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	notifier := newSerialVoiceNotifier(func(ctx context.Context, message string) error {
		started <- message
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, io.Discard)
	t.Cleanup(func() { _ = notifier.Close() })

	if err := notifier.Notify("first"); err != nil {
		t.Fatal(err)
	}
	wantStarted(t, started, "first")
	if err := notifier.Notify("second"); err != nil {
		t.Fatal(err)
	}
	if err := notifier.Notify("third"); err != nil {
		t.Fatal(err)
	}

	release <- struct{}{}
	wantStarted(t, started, "second")
	release <- struct{}{}
	wantStarted(t, started, "third")
	release <- struct{}{}
}

func TestSerialVoiceNotifierSerializesConcurrentNotifications(t *testing.T) {
	const notificationCount = 24

	release := make(chan struct{})
	started := make(chan string, notificationCount)
	var active atomic.Int32
	var maximumActive atomic.Int32
	notifier := newSerialVoiceNotifier(func(ctx context.Context, message string) error {
		current := active.Add(1)
		for {
			maximum := maximumActive.Load()
			if current <= maximum || maximumActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		defer active.Add(-1)
		started <- message
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, io.Discard)
	t.Cleanup(func() { _ = notifier.Close() })

	errCh := make(chan error, notificationCount)
	var enqueue sync.WaitGroup
	for i := 0; i < notificationCount; i++ {
		message := fmt.Sprintf("message-%02d", i)
		enqueue.Add(1)
		go func() {
			defer enqueue.Done()
			errCh <- notifier.Notify(message)
		}()
	}
	enqueue.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("Notify() error = %v", err)
		}
	}

	close(release)
	seen := make(map[string]int, notificationCount)
	for i := 0; i < notificationCount; i++ {
		select {
		case message := <-started:
			seen[message]++
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out after %d of %d notifications", i, notificationCount)
		}
	}
	if err := notifier.Close(); err != nil {
		t.Fatal(err)
	}
	if got := maximumActive.Load(); got != 1 {
		t.Fatalf("maximum active speakers = %d, want 1", got)
	}
	for i := 0; i < notificationCount; i++ {
		message := fmt.Sprintf("message-%02d", i)
		if got := seen[message]; got != 1 {
			t.Errorf("speaker calls for %q = %d, want 1", message, got)
		}
	}
}

func TestSerialVoiceNotifierContinuesAfterSpeakerError(t *testing.T) {
	var log bytes.Buffer
	started := make(chan string, 2)
	notifier := newSerialVoiceNotifier(func(_ context.Context, message string) error {
		started <- message
		if message == "broken" {
			return errors.New("speaker broke")
		}
		return nil
	}, &log)
	t.Cleanup(func() { _ = notifier.Close() })

	if err := notifier.Notify("broken"); err != nil {
		t.Fatal(err)
	}
	if err := notifier.Notify("next"); err != nil {
		t.Fatal(err)
	}
	wantStarted(t, started, "broken")
	wantStarted(t, started, "next")
	if err := notifier.Close(); err != nil {
		t.Fatal(err)
	}
	if got := log.String(); !strings.Contains(got, "speaker broke") {
		t.Fatalf("log = %q, want speaker error", got)
	}
}

func TestSerialVoiceNotifierCloseCancelsActiveAndDiscardsPending(t *testing.T) {
	started := make(chan string, 2)
	cancelled := make(chan struct{})
	notifier := newSerialVoiceNotifier(func(ctx context.Context, message string) error {
		started <- message
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}, io.Discard)

	if err := notifier.Notify("active"); err != nil {
		t.Fatal(err)
	}
	wantStarted(t, started, "active")
	if err := notifier.Notify("pending"); err != nil {
		t.Fatal(err)
	}

	const closerCount = 12
	errCh := make(chan error, closerCount)
	var closers sync.WaitGroup
	for i := 0; i < closerCount; i++ {
		closers.Add(1)
		go func() {
			defer closers.Done()
			errCh <- notifier.Close()
		}()
	}
	closers.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("Close() returned before the active speaker observed cancellation")
	}
	select {
	case message := <-started:
		t.Fatalf("pending message %q was spoken", message)
	default:
	}
	if err := notifier.Notify("too late"); err == nil || err.Error() != "voice notifier is closed" {
		t.Fatalf("Notify() after Close() error = %v, want %q", err, "voice notifier is closed")
	}
	if err := notifier.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestSerialVoiceNotifierConcurrentNotifyAndClose(t *testing.T) {
	const notifyCount = 128

	notifier := newSerialVoiceNotifier(func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}, io.Discard)
	start := make(chan struct{})
	results := make(chan error, notifyCount)
	var notifies sync.WaitGroup
	for i := 0; i < notifyCount; i++ {
		notifies.Add(1)
		go func() {
			defer notifies.Done()
			<-start
			results <- notifier.Notify("racing")
		}()
	}
	closeResult := make(chan error, 1)
	go func() {
		<-start
		closeResult <- notifier.Close()
	}()

	close(start)
	notifyDone := make(chan struct{})
	go func() {
		notifies.Wait()
		close(notifyDone)
	}()
	select {
	case <-notifyDone:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent Notify calls hung during Close")
	}
	for i := 0; i < notifyCount; i++ {
		err := <-results
		if err != nil && !errors.Is(err, errVoiceNotifierClosed) {
			t.Fatalf("concurrent Notify() error = %v", err)
		}
	}
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung while racing with Notify")
	}
	if err := notifier.Notify("after close"); !errors.Is(err, errVoiceNotifierClosed) {
		t.Fatalf("Notify() after Close() error = %v, want %v", err, errVoiceNotifierClosed)
	}
}

func TestNativeVoiceNotifierUnavailable(t *testing.T) {
	want := errors.New("speech backend unavailable")
	var notifier VoiceNotifier = unavailableVoiceNotifier{err: want}

	if err := notifier.Notify("hello"); !errors.Is(err, want) {
		t.Fatalf("Notify() error = %v, want %v", err, want)
	}
	if err := notifier.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func wantStarted(t *testing.T, started <-chan string, want string) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("started message = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}
