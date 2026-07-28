# Ticket Manager Voice Notification Design

## Goal

Announce a completed ticket as `{prefix}:{ticket number}:완료` after the ticket manager has both persisted the completed state and closed the worker pane. Voice notifications are enabled by default, configurable per repository, and use native speech facilities across macOS, Linux, and Windows without adding a Go speech dependency.

## Configuration

The ticket-worker configuration gains two keys:

```yaml
version: 1
max_workers: 3
poll_interval: 30s
voice_notifications: true
voice_notification_prefix: ticket-manager
```

`voice_notifications` defaults to `true` when omitted. `voice_notification_prefix` defaults to `ticket-manager` when omitted. This preserves compatibility with configuration files created before this feature.

The prefix is trimmed when loaded and must not be empty. The existing configuration version remains `1` because the new keys are optional and have defined defaults.

## Voice Backend Selection

A focused voice notifier component selects one native backend for the current operating system:

- macOS: `say`
- Linux: `spd-say`, falling back to `espeak`
- Windows: Windows PowerShell or PowerShell Core using `System.Speech.Synthesis.SpeechSynthesizer`

Executable discovery uses direct path lookup and command arguments rather than a shell. This avoids shell interpolation of the configured prefix. If no supported executable is available, notification returns an error for logging and does not affect ticket state.

The notifier starts speech asynchronously and reaps the child process after it exits. Ticket scheduling therefore does not wait for speech playback.

## Manager Integration

The manager receives a notifier through `ManagerOptions`. Production wiring supplies the native cross-platform notifier; tests inject a recording notifier.

The manager constructs the message with `fmt.Sprintf("%s:%d:완료", prefix, ticket.ID)`. It invokes the notifier only after `closeOrAbsent` reports that the worker pane is closed or absent, immediately before clearing the completed slot. Placing the call there ensures close retries do not produce duplicate announcements.

Disabling `voice_notifications` skips notification entirely. Notification errors are written to the manager log with ticket context, but the completed ticket and closed pane remain successful outcomes. A speech failure is never retried because retries could create delayed or duplicate announcements and speech is a best-effort side effect.

The guarantee is at-most-once per live manager slot. A process crash between pane closure and notification can lose an announcement; persistent exactly-once delivery is outside this feature's scope.

## Testing

Configuration tests cover explicit values, omitted-key defaults, disabling notifications, and rejection of an empty prefix.

Notifier tests cover backend selection and argument construction without invoking host audio. They verify macOS, Linux fallback order, Windows PowerShell selection, unsupported operating systems, and missing executables.

Manager tests cover one notification after a successful close, the exact `{prefix}:{ticket number}:완료` message, no notification while closing fails and retries, no notification when disabled, and notification errors not preventing slot cleanup.

## Scope

This feature does not add volume, voice, language, or arbitrary command-template settings. It does not announce manual `ticket-worker done` transitions; it applies only to completions owned by the running ticket manager.
