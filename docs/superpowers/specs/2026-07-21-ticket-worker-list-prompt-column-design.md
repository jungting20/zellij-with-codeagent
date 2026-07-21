# Ticket Worker List Prompt Column Design

## Goal

Include each ticket's full coding-agent prompt in the plain-text
`ticket-worker list` output without breaking the existing one-ticket-per-line
format.

## Output Format

Each non-JSON list row remains tab-separated and gains the prompt as its fifth
and final field:

```text
ID<TAB>Status<TAB>Title<TAB>PlanPath<TAB>Prompt
```

The prompt field escapes characters that could make the row ambiguous:

- backslash becomes `\\`
- newline becomes `\n`
- tab becomes `\t`
- carriage return becomes `\r`

Other characters, including Unicode text, remain unchanged. Escaping the
backslash first distinguishes a literal `\n` sequence from an encoded newline.

## Compatibility

This change affects only non-JSON output from `ticket-worker list`. JSON list
output already includes the `prompt` property and remains unchanged. Detailed
single-ticket output, status filtering, ordering, and the `No tickets.` message
also remain unchanged.

## Implementation

Add a small list-field escaping helper in the ticket-worker CLI package and use
it when appending `Ticket.Prompt` to each row produced by `reportTickets`. No
store, schema, transport, runtime, or Zellij behavior changes are required.

## Testing

Add focused CLI-package tests for plain-text list rendering. The tests verify
that the prompt is the final tab-separated field and that backslashes,
newlines, tabs, and carriage returns are escaped while Unicode text is
preserved. Existing package and repository tests must continue to pass.
