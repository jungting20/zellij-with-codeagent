package voice

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf16"
)

type speechBackend struct {
	path string
	args func(string) []string
	run  func(context.Context, string, []string) error
}

func (b speechBackend) speak(ctx context.Context, message string) error {
	run := b.run
	if run == nil {
		run = func(ctx context.Context, path string, args []string) error {
			return exec.CommandContext(ctx, path, args...).Run()
		}
	}
	return normalizeSpeechError(ctx, run(ctx, b.path, b.args(message)))
}

func normalizeSpeechError(ctx context.Context, err error) error {
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

	unixMessageArgs := func(message string) []string { return []string{"--", message} }
	spdSayArgs := func(message string) []string { return []string{"--wait", "--", message} }
	windowsArgs := func(message string) []string {
		encodedMessage := base64.StdEncoding.EncodeToString([]byte(message))
		script := fmt.Sprintf("$message = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')); Add-Type -AssemblyName System.Speech; $speaker = New-Object System.Speech.Synthesis.SpeechSynthesizer; $speaker.Speak($message)", encodedMessage)
		return []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShellCommand(script)}
	}

	var candidates []candidate
	switch goos {
	case "darwin":
		candidates = []candidate{{name: "say", args: unixMessageArgs}}
	case "linux":
		candidates = []candidate{{name: "spd-say", args: spdSayArgs}, {name: "espeak", args: unixMessageArgs}}
	case "windows":
		candidates = []candidate{{name: "powershell.exe", args: windowsArgs}, {name: "pwsh.exe", args: windowsArgs}}
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
