package zellij

func newCommand(binary, session string, args ...string) CommandSpec {
	spec := CommandSpec{Name: binary}
	if spec.Name == "" {
		spec.Name = defaultBinary
	}

	if session != "" {
		spec.Args = append(spec.Args, "--session", session)
	}
	spec.Args = append(spec.Args, args...)

	return spec
}

func newActionCommand(binary, session, action string, args ...string) CommandSpec {
	actionArgs := []string{"action", action}
	actionArgs = append(actionArgs, args...)
	return newCommand(binary, session, actionArgs...)
}

func createPaneCommand(binary, session string, req CreatePaneRequest) CommandSpec {
	args := make([]string, 0, 8+len(req.Command))
	if req.Name != "" {
		args = append(args, "--name", req.Name)
	}
	if req.CWD != "" {
		args = append(args, "--cwd", req.CWD)
	}
	if len(req.Command) > 0 {
		args = append(args, "--")
		args = append(args, req.Command...)
	}

	return newActionCommand(binary, session, "new-pane", args...)
}

func closePaneCommand(binary, session string, id PaneID) CommandSpec {
	return newActionCommand(binary, session, "close-pane", "--pane-id", string(id))
}

func pasteCommand(binary, session string, id PaneID, text string) CommandSpec {
	return newActionCommand(binary, session, "paste", "--pane-id", string(id), text)
}

func sendEnterCommand(binary, session string, id PaneID) CommandSpec {
	return newActionCommand(binary, session, "send-keys", "--pane-id", string(id), "Enter")
}

func listPanesCommand(binary, session string) CommandSpec {
	return newActionCommand(binary, session, "list-panes", "--json")
}

func dumpScreenCommand(binary, session string, req DumpScreenRequest) CommandSpec {
	args := []string{"--pane-id", string(req.PaneID)}
	if req.Full {
		args = append(args, "--full")
	}
	if req.ANSI {
		args = append(args, "--ansi")
	}

	return newActionCommand(binary, session, "dump-screen", args...)
}

func subscribeCommand(binary, session string, req SubscribeRequest) CommandSpec {
	args := []string{"subscribe", "--pane-id", string(req.PaneID)}
	if req.JSON {
		args = append(args, "--format", "json")
	}
	if req.ANSI {
		args = append(args, "--ansi")
	}

	return newCommand(binary, session, args...)
}
