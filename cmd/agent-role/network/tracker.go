package network

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"zellij-with-codeagent/cmd/agent-role/ui"
)

type requestInfo struct {
	Method    string
	URL       string
	Timestamp time.Time
}

// Run starts the network tracker using chromedp to capture real network traffic
func Run(targetURL string) {
	if targetURL == "" {
		log.Fatal("Error: target URL is empty")
	}

	// Parse URL to display host info
	parsed, err := url.Parse(targetURL)
	if err != nil {
		log.Fatalf("Invalid URL: %v", err)
	}
	host := parsed.Host

	// Create headless chrome context
	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()

	// Track requests to match them with responses
	var mu sync.Mutex
	requests := make(map[network.RequestID]*requestInfo)
	var logs []string
	maxLogs := 15

	// Set up target listener
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventRequestWillBeSent:
			mu.Lock()
			if ev == nil || ev.Request == nil {
				mu.Unlock()
				return
			}
			reqURL := ev.Request.URL
			if parsedReq, err := url.Parse(reqURL); err == nil && parsedReq != nil {
				path := parsedReq.Path
				if parsedReq.RawQuery != "" {
					path += "?" + parsedReq.RawQuery
				}
				requests[ev.RequestID] = &requestInfo{
					Method:    ev.Request.Method,
					URL:       path,
					Timestamp: time.Now(),
				}
			}
			mu.Unlock()

		case *network.EventResponseReceived:
			mu.Lock()
			if ev == nil || ev.Response == nil {
				mu.Unlock()
				return
			}
			req, exists := requests[ev.RequestID]
			var method, path string
			var latency int64

			if exists && req != nil {
				method = req.Method
				path = req.URL
				latency = time.Since(req.Timestamp).Milliseconds()
				delete(requests, ev.RequestID)
			} else {
				method = "GET"
				if parsedReq, err := url.Parse(ev.Response.URL); err == nil && parsedReq != nil {
					path = parsedReq.Path
				} else {
					path = ev.Response.URL
				}
				latency = 0
			}

			// Format network log line
			sizeKB := float64(ev.Response.EncodedDataLength) / 1024.0
			logLine := formatNetworkLog(method, path, int(ev.Response.Status), sizeKB, int(latency))
			
			logs = append(logs, logLine)
			if len(logs) > maxLogs {
				logs = logs[1:]
			}

			// Redraw screen
			ui.ClearScreen()
			fmt.Printf("%s==================================================%s\n", ui.Blue, ui.Reset)
			fmt.Printf("%s%s[NETWORK TRACKER]%s Real-time Monitoring...\n", ui.Bold, ui.Green, ui.Reset)
			fmt.Printf("%s==================================================%s\n", ui.Blue, ui.Reset)
			fmt.Printf("%sTarget URL:%s %s\n", ui.Bold, ui.Reset, targetURL)
			fmt.Printf("%sHost:%s       %s\n", ui.Bold, ui.Reset, host)
			fmt.Printf("%sStatus:%s     %sRUNNING%s\n\n", ui.Bold, ui.Reset, ui.Green, ui.Reset)

			fmt.Printf("%s[REAL NETWORK TRAFFIC LOGS]%s\n", ui.Bold+ui.Yellow, ui.Reset)
			for _, l := range logs {
				fmt.Println(l)
			}
			mu.Unlock()
		}
	})

	// Initial render
	ui.ClearScreen()
	fmt.Printf("%s==================================================%s\n", ui.Blue, ui.Reset)
	fmt.Printf("%s%s[NETWORK TRACKER]%s Connecting browser...\n", ui.Bold, ui.Green, ui.Reset)
	fmt.Printf("%s==================================================%s\n", ui.Blue, ui.Reset)
	fmt.Printf("Navigating to: %s\n", targetURL)

	// Run tasks to enable network and navigate to the target URL
	err = chromedp.Run(ctx,
		network.Enable(),
		chromedp.Navigate(targetURL),
	)
	if err != nil {
		log.Fatalf("Error running chromedp: %v", err)
	}

	// Keep browser alive and listen to events infinitely
	select {}
}

// formatNetworkLog formats a single network log with colors based on status code
func formatNetworkLog(method string, path string, status int, size float64, latency int) string {
	statusColor := ui.Green
	if status >= 400 && status < 500 {
		statusColor = ui.Yellow
	} else if status >= 500 {
		statusColor = ui.Red
	}

	methodColor := ui.Cyan
	if method == "POST" {
		methodColor = ui.Magenta
	} else if method == "PUT" {
		methodColor = ui.Yellow
	} else if method == "DELETE" {
		methodColor = ui.Red
	}

	// Shorten path if it's too long
	if len(path) > 40 {
		path = path[:37] + "..."
	}

	timestamp := time.Now().Format("15:04:05.000")
	return fmt.Sprintf("%s%s%s %s%s%-6s%s %s%-40s%s %s%s%d%s (%.2f KB) - %dms",
		ui.DarkGray, timestamp, ui.Reset,
		methodColor, ui.Bold, method, ui.Reset,
		ui.Gray, path, ui.Reset,
		statusColor, ui.Bold, status, ui.Reset,
		size, latency,
	)
}
