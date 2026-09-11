# Dashboard worktree agents

Select an agent in `zellij-agent agent dashboard` and press `w`. A new Git
branch and worktree are created from that agent directory's repository HEAD.
The name is `<repository-basename>-HHMMSS` in local time, for example
`project-143052`. The directory is under
`os.TempDir()/zellij-agent-worktrees/` (the user temporary directory on macOS).
Uncommitted changes are not copied. Name collisions fail without overwriting
an existing worktree or branch; retry in a later second.

After creation, a small dashboard popup reuses the `list-selector` list,
initial prompt, voice and yolo options. Enter starts the selected agent through
the daemon in a new pane in the parent's Zellij tab. Esc closes the picker.
The created worktree is retained after cancellation, launch failure or agent
exit; remove it explicitly with Git when it is no longer needed.

Child agents appear immediately below their parent with `↳` indentation within
the same pin/session/tab group. Nested children are supported. When the parent
is closed, absent, or in another pin group, the child displays its parent pane
ID. Pinning remains independent, and closing a parent does not close children.
The relationship survives daemon restart; see [persistence](daemon-persistence.md).

The default role `zellij-agent role agent-worktree <path>` creates a worktree
and opens the selector in the current terminal. It requires Git, a repository
with a HEAD commit, and the `zellij-agent` executable on PATH. Dashboard child
creation additionally requires a running Zellij session and the updated daemon.
