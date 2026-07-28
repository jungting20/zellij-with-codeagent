package voice

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
		{name: "macOS uses say", goos: "darwin", executables: map[string]string{"say": "/usr/bin/say"}, wantPath: "/usr/bin/say", wantArgs: []string{"--", "hello"}},
		{name: "Linux prefers spd-say", goos: "linux", executables: map[string]string{"spd-say": "/usr/bin/spd-say", "espeak": "/usr/bin/espeak"}, wantPath: "/usr/bin/spd-say", wantArgs: []string{"--wait", "--", "hello"}},
		{name: "Linux falls back to espeak", goos: "linux", executables: map[string]string{"espeak": "/usr/bin/espeak"}, wantPath: "/usr/bin/espeak", wantArgs: []string{"--", "hello"}},
		{name: "Windows prefers Windows PowerShell", goos: "windows", executables: map[string]string{"powershell.exe": `C:\Windows\powershell.exe`, "pwsh.exe": `C:\Program Files\PowerShell\7\pwsh.exe`}, wantPath: `C:\Windows\powershell.exe`},
		{name: "Windows falls back to PowerShell Core", goos: "windows", executables: map[string]string{"pwsh.exe": `C:\Program Files\PowerShell\7\pwsh.exe`}, wantPath: `C:\Program Files\PowerShell\7\pwsh.exe`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := resolveSpeechBackend(tt.goos, mapLookPath(tt.executables))
			if err != nil {
				t.Fatal(err)
			}
			if backend.path != tt.wantPath {
				t.Fatalf("path = %q, want %q", backend.path, tt.wantPath)
			}
			if tt.wantArgs != nil && !reflect.DeepEqual(backend.args("hello"), tt.wantArgs) {
				got := backend.args("hello")
				t.Fatalf("args = %q, want %q", got, tt.wantArgs)
			}
		})
	}
}

func TestSpeechBackendWindowsEncodedCommandPreservesMessage(t *testing.T) {
	message := "hello'; Write-Error 'owned'\n$([char]0x41) 안녕 🔊"
	backend, err := resolveSpeechBackend("windows", mapLookPath(map[string]string{"powershell.exe": `C:\Windows\powershell.exe`}))
	if err != nil {
		t.Fatal(err)
	}
	args := backend.args(message)
	if want := []string{"-NoProfile", "-NonInteractive", "-EncodedCommand"}; len(args) != 4 || !reflect.DeepEqual(args[:3], want) {
		t.Fatalf("args = %q, want fixed encoded-command arguments", args)
	}
	for _, arg := range args {
		if strings.Contains(arg, message) {
			t.Fatalf("raw message appears in process argument %q", arg)
		}
	}
	command := decodeUTF16LEBase64(t, args[3])
	const prefix = "$message = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('"
	rest, ok := strings.CutPrefix(command, prefix)
	if !ok {
		t.Fatalf("decoded command %q does not safely decode message", command)
	}
	encoded, rest, ok := strings.Cut(rest, "')); ")
	if !ok {
		t.Fatalf("decoded command %q does not end embedded base64", command)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != message {
		t.Fatalf("embedded message = %q, %v; want %q", decoded, err, message)
	}
	if strings.Contains(rest, "$args[0]") || !strings.Contains(rest, "$speaker.Speak($message)") {
		t.Fatalf("decoded command uses unsafe speech invocation: %q", command)
	}
}

func TestResolveSpeechBackendReportsMissingExecutables(t *testing.T) {
	_, err := resolveSpeechBackend("linux", mapLookPath(nil))
	if err == nil || !strings.Contains(err.Error(), "spd-say") || !strings.Contains(err.Error(), "espeak") {
		t.Fatalf("resolveSpeechBackend() error = %v, want unavailable candidates", err)
	}
}

func TestSpeechBackendPreservesCommandFailureAndNormalizesCancellation(t *testing.T) {
	processErr := errors.New("speech process failed")
	tests := []struct {
		name   string
		cancel bool
		want   error
	}{
		{name: "command failure", want: processErr},
		{name: "cancellation overrides command failure", cancel: true, want: context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := speechBackend{
				path: "/test/speaker",
				args: func(string) []string { return []string{"--", "hello"} },
				run: func(runCtx context.Context, path string, args []string) error {
					if runCtx != ctx || path != "/test/speaker" || !reflect.DeepEqual(args, []string{"--", "hello"}) {
						t.Fatalf("runner arguments = (%v, %q, %q)", runCtx, path, args)
					}
					if tt.cancel {
						cancel()
					}
					return processErr
				},
			}
			if got := backend.speak(ctx, "hello"); got != tt.want {
				t.Fatalf("speak() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSpeechBackendRunsFailingAndCancelledChildProcesses(t *testing.T) {
	// Normalize sanitizer delay in recursively executed test binaries so the
	// deadline measures backend startup and cancellation rather than race cleanup.
	t.Setenv("GORACE", "atexit_sleep_ms=0")

	childBackend := func(mode, marker string) speechBackend {
		return speechBackend{
			path: os.Args[0],
			args: func(string) []string {
				return []string{"-test.run=^TestSpeechBackendChildProcess$", "--", mode, marker}
			},
		}
	}

	t.Run("failing child process", func(t *testing.T) {
		err := childBackend("fail", "").speak(context.Background(), "ignored")
		if err == nil {
			t.Fatal("speak() error = nil, want child-process failure")
		}
	})

	t.Run("cancelled child process", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "started")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		errs := make(chan error, 1)
		go func() { errs <- childBackend("block", marker).speak(ctx, "ignored") }()
		waitForFile(t, marker)
		cancel()
		select {
		case err := <-errs:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("speak() error = %v, want %v", err, context.Canceled)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for cancelled child process")
		}
	})
}

func TestSpeechBackendChildProcess(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator == -1 || len(os.Args) < separator+2 {
		return
	}
	switch os.Args[separator+1] {
	case "fail":
		os.Exit(23)
	case "block":
		if len(os.Args) < separator+3 {
			os.Exit(24)
		}
		if err := os.WriteFile(os.Args[separator+2], []byte("started"), 0o600); err != nil {
			os.Exit(25)
		}
		for {
			time.Sleep(10 * time.Millisecond)
		}
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

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for child process marker %q", path)
		}
		time.Sleep(time.Millisecond)
	}
}

func decodeUTF16LEBase64(t *testing.T, value string) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	return string(utf16.Decode(units))
}
