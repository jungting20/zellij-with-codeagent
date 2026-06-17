package console

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"zellij-with-codeagent/cmd/agent-role/ui"
)

// Run parses console tracker arguments and starts browser console monitoring.
func Run(args []string) int {
	fs := flag.NewFlagSet("console-tracker", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	urlPtr := fs.String("url", "", "Target URL to track console logs")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	if urlPtr == nil || *urlPtr == "" {
		fmt.Fprintln(os.Stderr, "Error: --url parameter is required for console-tracker")
		fs.Usage()
		return 1
	}

	return run(*urlPtr)
}

// run starts the console tracker using chromedp to capture real browser console logs.
func run(targetURL string) int {
	if targetURL == "" {
		fmt.Fprintln(os.Stderr, "Error: target URL is empty")
		return 1
	}

	// Parse URL to display host info
	parsed, err := url.Parse(targetURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid URL: %v\n", err)
		return 1
	}
	host := parsed.Host

	// Create headless chrome context
	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()

	var mu sync.Mutex
	var logs []string
	maxLogs := 15

	// Set up target listener for console events
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		if ev, ok := ev.(*runtime.EventConsoleAPICalled); ok {
			mu.Lock()
			if ev == nil {
				mu.Unlock()
				return
			}
			// Parse log level
			level := strings.ToUpper(string(ev.Type))
			if level == "WARNING" {
				level = "WARN"
			}

			// Concatenate console argument values
			var msgParts []string
			for _, arg := range ev.Args {
				if arg != nil {
					if len(arg.Value) > 0 {
						msgParts = append(msgParts, strings.Trim(string(arg.Value), `"`))
					} else if arg.Description != "" {
						msgParts = append(msgParts, arg.Description)
					}
				}
			}
			message := strings.Join(msgParts, " ")
			if message == "" {
				message = "(empty message)"
			}

			// Format log line with colors
			logLine := formatConsoleLog(level, message)
			logs = append(logs, logLine)
			if len(logs) > maxLogs {
				logs = logs[1:]
			}

			// Redraw screen
			ui.ClearScreen()
			fmt.Printf("%s==================================================%s\n", ui.Blue, ui.Reset)
			fmt.Printf("%s%s[CONSOLE TRACKER]%s Real-time Monitoring...\n", ui.Bold, ui.Green, ui.Reset)
			fmt.Printf("%s==================================================%s\n", ui.Blue, ui.Reset)
			fmt.Printf("%sTarget URL:%s %s\n", ui.Bold, ui.Reset, targetURL)
			fmt.Printf("%sHost:%s       %s\n", ui.Bold, ui.Reset, host)
			fmt.Printf("%sStatus:%s     %sRUNNING%s\n\n", ui.Bold, ui.Reset, ui.Green, ui.Reset)

			fmt.Printf("%s[REAL BROWSER CONSOLE LOGS]%s\n", ui.Bold+ui.Yellow, ui.Reset)
			for _, l := range logs {
				fmt.Println(l)
			}
			mu.Unlock()
		}
	})

	// Initial render
	ui.ClearScreen()
	fmt.Printf("%s==================================================%s\n", ui.Blue, ui.Reset)
	fmt.Printf("%s%s[CONSOLE TRACKER]%s Connecting browser...\n", ui.Bold, ui.Green, ui.Reset)
	fmt.Printf("%s==================================================%s\n", ui.Blue, ui.Reset)
	fmt.Printf("Navigating to: %s\n", targetURL)

	// Run tasks to enable runtime and navigate to target URL
	err = chromedp.Run(ctx,
		runtime.Enable(),
		chromedp.Navigate(targetURL),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running chromedp: %v\n", err)
		return 1
	}

	// Keep browser alive and listen to events infinitely
	select {}
}

// formatConsoleLog formats a single browser console log with custom level colors
func formatConsoleLog(level string, message string) string {
	levelColor := ui.Gray
	msgColor := ui.Gray
	switch level {
	case "DEBUG":
		levelColor = ui.Cyan
		msgColor = ui.DarkGray
	case "LOG", "INFO":
		levelColor = ui.Green
		msgColor = ui.Reset
	case "WARN":
		levelColor = ui.Yellow + ui.Bold
		msgColor = ui.Yellow
	case "ERROR":
		levelColor = ui.Red + ui.Bold
		msgColor = ui.Red
	}

	timestamp := time.Now().Format("15:04:05.000")
	return fmt.Sprintf("%s %s [%s%-5s%s] %s%s%s",
		ui.DarkGray, timestamp,
		levelColor, level, ui.Reset,
		msgColor, message, ui.Reset,
	)
}
