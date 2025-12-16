//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a client that connects to the EventEmitter server example
// and displays custom events, progress events, and streaming text events.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/client/sse"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/sirupsen/logrus"
)

const (
	defaultEndpoint  = "http://127.0.0.1:8080/agui"
	requestTimeout   = 2 * time.Minute
	connectTimeout   = 30 * time.Second
	readTimeout      = 5 * time.Minute
	streamBufferSize = 100
)

var (
	endpoint = flag.String("endpoint", defaultEndpoint, "AG-UI SSE endpoint")
	prompt   = flag.String("prompt", "process my data", "User prompt to send")
)

func main() {
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║       EventEmitter Client - Node Custom Events Demo          ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Printf("\n📡 Connecting to: %s\n", *endpoint)
	fmt.Printf("📝 Sending prompt: %q\n\n", *prompt)

	if err := runDemo(*endpoint, *prompt); err != nil {
		fmt.Fprintf(os.Stderr, "\n❌ Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n✅ Demo completed successfully!")
}

func runDemo(endpoint, prompt string) error {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client := newSSEClient(endpoint)
	defer client.Close()

	payload := map[string]any{
		"threadId": "event-emitter-demo-thread",
		"runId":    fmt.Sprintf("run-%d", time.Now().UnixNano()),
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	}

	frames, errCh, err := client.Stream(sse.StreamOptions{Context: ctx, Payload: payload})
	if err != nil {
		return fmt.Errorf("failed to start SSE stream: %w", err)
	}

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("                         Event Stream")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	for frames != nil || errCh != nil {
		select {
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			evt, err := events.EventFromJSON(frame.Data)
			if err != nil {
				return fmt.Errorf("failed to parse event: %w", err)
			}
			displayEvent(evt)
		case err, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if err != nil {
				return fmt.Errorf("stream error: %w", err)
			}
		case <-ctx.Done():
			return fmt.Errorf("stream timeout: %w", ctx.Err())
		}
	}

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return nil
}

func newSSEClient(endpoint string) *sse.Client {
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	return sse.NewClient(sse.Config{
		Endpoint:       endpoint,
		ConnectTimeout: connectTimeout,
		ReadTimeout:    readTimeout,
		BufferSize:     streamBufferSize,
		Logger:         logger,
	})
}

func displayEvent(evt events.Event) {
	switch e := evt.(type) {
	case *events.RunStartedEvent:
		fmt.Printf("\n🚀 [run_started] Run started\n")
		fmt.Printf("   Thread: %s, Run: %s\n", e.ThreadID(), e.RunID())

	case *events.RunFinishedEvent:
		fmt.Printf("\n🏁 [run_finished] Run completed\n")
		fmt.Printf("   Thread: %s, Run: %s\n", e.ThreadID(), e.RunID())

	case *events.RunErrorEvent:
		fmt.Printf("\n❌ [run_error] Error: %s\n", e.Message)

	case *events.TextMessageStartEvent:
		fmt.Printf("\n💬 [text_message_start] Message started (ID: %s)\n", e.MessageID)

	case *events.TextMessageContentEvent:
		if strings.TrimSpace(e.Delta) != "" {
			fmt.Printf("   📝 %s", e.Delta)
		}

	case *events.TextMessageEndEvent:
		fmt.Printf("\n💬 [text_message_end] Message ended (ID: %s)\n", e.MessageID)

	case *events.ToolCallStartEvent:
		fmt.Printf("\n🔧 [tool_call_start] Tool: %s (ID: %s)\n", e.ToolCallName, e.ToolCallID)

	case *events.ToolCallArgsEvent:
		fmt.Printf("   Args: %s\n", e.Delta)

	case *events.ToolCallEndEvent:
		fmt.Printf("🔧 [tool_call_end] Tool call completed (ID: %s)\n", e.ToolCallID)

	case *events.ToolCallResultEvent:
		fmt.Printf("   Result: %s\n", e.Content)

	case *events.CustomEvent:
		displayCustomEvent(e)

	default:
		fmt.Printf("\n📨 [%s] Event received\n", evt.Type())
	}
}

func displayCustomEvent(e *events.CustomEvent) {
	// Parse the custom event based on its name
	switch {
	case strings.HasPrefix(e.Name, "workflow."):
		displayWorkflowEvent(e)
	case e.Name == "node.progress" || e.Name == "progress":
		displayProgressEvent(e)
	case e.Name == "node.text" || e.Name == "text":
		displayTextEvent(e)
	default:
		displayGenericCustomEvent(e)
	}
}

func displayWorkflowEvent(e *events.CustomEvent) {
	value, ok := e.Value.(map[string]any)
	if !ok {
		fmt.Printf("\n⚡ [custom] %s\n", e.Name)
		return
	}

	switch e.Name {
	case "workflow.started":
		fmt.Printf("\n🎬 [workflow.started] Workflow initiated\n")
		if ts, ok := value["timestamp"].(string); ok {
			fmt.Printf("   ⏰ Timestamp: %s\n", ts)
		}
		if input, ok := value["user_input"].(string); ok {
			fmt.Printf("   📥 User input: %q\n", input)
		}
		if version, ok := value["version"].(string); ok {
			fmt.Printf("   📌 Version: %s\n", version)
		}

	case "workflow.completed":
		fmt.Printf("\n🎉 [workflow.completed] Workflow finished\n")
		if ts, ok := value["timestamp"].(string); ok {
			fmt.Printf("   ⏰ Timestamp: %s\n", ts)
		}
		if result, ok := value["result"].(string); ok {
			fmt.Printf("   📤 Result: %s\n", result)
		}
		if success, ok := value["success"].(bool); ok {
			if success {
				fmt.Printf("   ✅ Status: Success\n")
			} else {
				fmt.Printf("   ❌ Status: Failed\n")
			}
		}
		if duration, ok := value["duration_ms"].(float64); ok {
			fmt.Printf("   ⏱️  Duration: %.0fms\n", duration)
		}
		if nodes, ok := value["nodes_visited"].([]any); ok {
			nodeNames := make([]string, len(nodes))
			for i, n := range nodes {
				nodeNames[i] = fmt.Sprintf("%v", n)
			}
			fmt.Printf("   🔗 Nodes: %s\n", strings.Join(nodeNames, " → "))
		}
	}
}

func displayProgressEvent(e *events.CustomEvent) {
	value, ok := e.Value.(map[string]any)
	if !ok {
		fmt.Printf("\n📊 [progress] Progress update\n")
		return
	}

	progress, _ := value["progress"].(float64)
	message, _ := value["message"].(string)
	nodeID, _ := value["nodeId"].(string)

	// Create progress bar
	barWidth := 30
	filled := int(progress / 100 * float64(barWidth))
	empty := barWidth - filled
	progressBar := strings.Repeat("█", filled) + strings.Repeat("░", empty)

	fmt.Printf("\r📊 [%s] %s %5.1f%% - %s", nodeID, progressBar, progress, message)
	if progress >= 100 {
		fmt.Println() // New line when complete
	}
}

func displayTextEvent(e *events.CustomEvent) {
	value, ok := e.Value.(map[string]any)
	if !ok {
		return
	}

	content, _ := value["content"].(string)
	nodeID, _ := value["nodeId"].(string)

	if content != "" {
		fmt.Printf("📝 [%s] %s", nodeID, content)
	}
}

func displayGenericCustomEvent(e *events.CustomEvent) {
	data, err := json.MarshalIndent(e.Value, "   ", "  ")
	if err != nil {
		fmt.Printf("\n⚡ [custom] %s\n", e.Name)
		return
	}
	fmt.Printf("\n⚡ [custom] %s:\n   %s\n", e.Name, string(data))
}
