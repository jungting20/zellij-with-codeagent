package coder

import (
	"fmt"
	"math/rand"
	"time"

	"zellij-with-codeagent/cmd/agent-role/ui"
)

// Run starts the coder agent visual dashboard simulation
func Run() {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	files := []string{
		"internal/zellij/client.go",
		"internal/runtime/manager.go",
		"internal/eventbus/bus.go",
		"cmd/agentd/main.go",
		"internal/supervisor/supervisor.go",
	}

	tasks := []string{
		"Refactoring RPC handlers for agentd supervisor",
		"Implementing async event subscription interface",
		"Optimizing terminal layout refresh mechanism",
		"Writing unit tests for eventbus message delivery",
		"Handling system signals for graceful shutdown",
	}

	logs := []string{
		"Analyzing workspace directory structure...",
		"Reading configuration parameters...",
		"Initializing Zellij developer workspace pane...",
		"Opening file for editing...",
		"Injecting reactive event listener handlers...",
		"Compiling current source modules...",
		"Lint checks: completed successfully with 0 warnings",
		"Invoking local unit testing suite...",
		"Analyzing coverage reports...",
		"Cleaning up temporary code artifacts...",
	}

	tick := 0
	progress := 0
	currentFileIdx := r.Intn(len(files))
	currentTaskIdx := r.Intn(len(tasks))

	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		ui.ClearScreen()

		fmt.Printf("%s==================================================%s\n", ui.Blue, ui.Reset)
		fmt.Printf("%s%s[CODER AGENT]%s Currently Processing Task...\n", ui.Bold, ui.Green, ui.Reset)
		fmt.Printf("%s==================================================%s\n", ui.Blue, ui.Reset)
		fmt.Printf("%sTask:%s     %s\n", ui.Bold, ui.Reset, tasks[currentTaskIdx])
		fmt.Printf("%sFile:%s     %s\n", ui.Bold, ui.Reset, files[currentFileIdx])

		progress += r.Intn(15) + 5
		if progress > 100 {
			progress = 100
		}

		progBar := ui.GenerateProgressBar(progress, 20)
		fmt.Printf("%sProgress:%s [%s] %d%%\n\n", ui.Bold, ui.Reset, progBar, progress)

		fmt.Printf("%s[ACTIVITY LOGS]%s\n", ui.Bold+ui.Yellow, ui.Reset)

		logLimit := tick
		if logLimit > len(logs) {
			logLimit = len(logs)
		}
		for i := 0; i < logLimit; i++ {
			fmt.Printf("%s %s - %s\n", ui.DarkGray, time.Now().Format("15:04:05"), logs[i])
		}

		if progress >= 100 {
			fmt.Printf("\n%s[SUCCESS]%s Task completed! Standing by for next prompt...\n", ui.Bold+ui.Green, ui.Reset)
			time.Sleep(3 * time.Second)
			progress = 0
			currentFileIdx = r.Intn(len(files))
			currentTaskIdx = r.Intn(len(tasks))
			tick = 0
			continue
		}

		tick++
	}
}
