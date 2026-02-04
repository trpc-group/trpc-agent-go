//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a Skill-driven pattern for dynamically switching
// structured output JSON schema at runtime:
//
//   - The model chooses a Skill based on user input.
//   - The model loads the Skill content, extracts a JSON Schema, and calls a
//     user-defined set_output_schema tool to update invocation.StructuredOutput.
//   - Subsequent model calls in the same invocation are constrained by that schema.
//   - The final JSON is extracted into event.StructuredOutput (untyped map/slice/etc).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	flagModel      = flag.String("model", "deepseek-chat", "model name (OpenAI-compatible)")
	flagStreaming  = flag.Bool("streaming", false, "stream responses")
	flagTraceTools = flag.Bool("trace_tools", true, "print tool call/response trace")
)

const (
	appName          = "skill-dynamic-schema-demo"
	defaultSkillsDir = "skills"

	// OutputKey is only used to make sure OutputResponseProcessor is installed
	// (so we can read event.StructuredOutput without changing the framework).
	outputKey = "__so_tmp__"
)

const instructionText = `
You are a tool-using agent.

You have two skills available: plan_route and recommend_poi.

For every user request:
- Choose exactly one skill to run.
  - If the user explicitly mentions "plan_route" or "recommend_poi", use that one.
  - Otherwise, use plan_route for route/ETA/distance requests, and recommend_poi for POI/city/recommendation requests.
- Perform the following steps using tool calls only (no assistant content):
  1) Call skill_load for the chosen skill.
  2) From the loaded skill content, find the JSON schema under the section "Output JSON Schema".
  3) Call set_output_schema with {"schema": <that JSON schema object>}.
     - Do NOT call set_output_schema without "schema".
     - If set_output_schema returns {"ok": false, ...}, extract the schema again and retry.
     - Do not proceed to step 4 until set_output_schema returns {"ok": true, ...}.
  4) Call skill_run with {"skill":"<chosen skill>","command":"cat result.json"}.
- Finally, return ONLY the JSON object from step 4 stdout. No extra text.

Do not output analysis.
`

func main() {
	flag.Parse()

	fmt.Println("Skill-Driven Dynamic Structured Output (JSON Schema)")
	fmt.Printf("Model: %s\n", *flagModel)
	fmt.Printf("Streaming: %t\n", *flagStreaming)
	fmt.Printf("Trace tools: %t\n", *flagTraceTools)
	fmt.Println("Type 'exit' to quit.")
	fmt.Println(strings.Repeat("=", 60))

	if err := run(); err != nil {
		fmt.Printf("run failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	modelInstance := openai.New(*flagModel)

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	skillsRoot := filepath.Join(cwd, defaultSkillsDir)
	repo, err := skill.NewFSRepository(skillsRoot)
	if err != nil {
		return fmt.Errorf("skills repo: %w", err)
	}

	exec := localexec.New()
	genConfig := model.GenerationConfig{
		Temperature: temperatureForModel(*flagModel),
		Stream:      *flagStreaming,
	}

	a := llmagent.New(
		"skill-dynamic-schema",
		llmagent.WithModel(modelInstance),
		llmagent.WithInstruction(instructionText),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithSkills(repo),
		llmagent.WithCodeExecutor(exec),
		llmagent.WithTools([]tool.Tool{&setOutputSchemaTool{}}),
		// Key point: install OutputResponseProcessor without a static schema.
		llmagent.WithOutputKey(outputKey),
	)

	r := runner.NewRunner(appName, a, runner.WithSessionService(inmemory.NewSessionService()))
	defer r.Close()

	userID := "user"
	sessionID := fmt.Sprintf("so-dyn-%d", time.Now().Unix())
	fmt.Printf("Session: %s\n", sessionID)
	fmt.Println()
	fmt.Println("Example prompts:")
	fmt.Println(`- "Plan a route from A to B and return distance and ETA. (Use plan_route for the output format.)"`)
	fmt.Println(`- "Recommend a coffee shop POI in Shenzhen. (Use recommend_poi for the output format.)"`)
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if strings.EqualFold(text, "exit") {
			return nil
		}

		evCh, err := r.Run(ctx, userID, sessionID, model.NewUserMessage(text))
		if err != nil {
			fmt.Printf("error: %v\n\n", err)
			continue
		}

		var structured any
		var role model.Role
		toolNameByID := make(map[string]string)
		seenToolCall := make(map[string]struct{})
		seenToolResult := make(map[string]struct{})
		for ev := range evCh {
			if ev == nil {
				continue
			}
			if ev.Error != nil {
				fmt.Printf("\nerror: %s\n", ev.Error.Message)
				break
			}
			if ev.StructuredOutput != nil {
				structured = ev.StructuredOutput
			}
			if len(ev.Choices) == 0 {
				continue
			}

			if *flagTraceTools {
				for _, choice := range ev.Choices {
					printToolCalls(choice.Message.ToolCalls, toolNameByID, seenToolCall)
					printToolCalls(choice.Delta.ToolCalls, toolNameByID, seenToolCall)

					printToolResult(choice.Message, toolNameByID, seenToolResult)
					printToolResult(choice.Delta, toolNameByID, seenToolResult)
				}
			}

			choice := ev.Choices[0]
			if choice.Message.Role != "" {
				role = choice.Message.Role
			} else if choice.Delta.Role != "" {
				role = choice.Delta.Role
			}
			if role != model.RoleAssistant {
				continue
			}

			if *flagStreaming {
				if s := choice.Delta.Content; s != "" {
					fmt.Print(s)
				}
			} else if s := choice.Message.Content; s != "" {
				fmt.Println(s)
			}
		}
		fmt.Println()

		if structured != nil {
			if b, err := json.MarshalIndent(structured, "", "  "); err == nil {
				fmt.Printf("event.StructuredOutput:\n%s\n\n", string(b))
			} else {
				fmt.Printf("event.StructuredOutput: %#v\n\n", structured)
			}
		} else {
			fmt.Println("event.StructuredOutput: <nil>")
			fmt.Println()
		}
	}

	return scanner.Err()
}

func floatPtr(v float64) *float64 { return &v }

func temperatureForModel(name string) *float64 {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "gpt-5") {
		// gpt-5 only supports the default temperature; omit it to avoid 400s.
		return nil
	}
	return floatPtr(0.2)
}

func printToolCalls(
	toolCalls []model.ToolCall,
	toolNameByID map[string]string,
	seen map[string]struct{},
) {
	for _, toolCall := range toolCalls {
		if toolCall.ID == "" {
			continue
		}
		if _, ok := seen[toolCall.ID]; ok {
			continue
		}
		seen[toolCall.ID] = struct{}{}
		toolNameByID[toolCall.ID] = toolCall.Function.Name

		fmt.Printf(
			"tool_call: %s %s (id=%s)\n",
			toolIcon(toolCall.Function.Name),
			toolCall.Function.Name,
			toolCall.ID,
		)
		if len(toolCall.Function.Arguments) > 0 {
			fmt.Printf("  args: %s\n", formatInlineJSON(toolCall.Function.Arguments))
		}
	}
}

func printToolResult(
	msg model.Message,
	toolNameByID map[string]string,
	seen map[string]struct{},
) {
	if msg.Role != model.RoleTool || msg.ToolID == "" {
		return
	}
	if _, ok := seen[msg.ToolID]; ok {
		return
	}
	seen[msg.ToolID] = struct{}{}

	name := strings.TrimSpace(msg.ToolName)
	if name == "" {
		name = toolNameByID[msg.ToolID]
	}
	if name == "" {
		name = "unknown"
	}

	content := strings.TrimSpace(msg.Content)
	if content == "" {
		content = "<empty>"
	}
	fmt.Printf(
		"tool_result: %s %s (id=%s): %s\n",
		toolIcon(name),
		name,
		msg.ToolID,
		formatToolResult(content),
	)
}

func formatToolResult(content string) string {
	const maxLen = 400
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}

func formatInlineJSON(b []byte) string {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return formatToolResult(strings.TrimSpace(string(b)))
	}
	compact, err := json.Marshal(v)
	if err != nil {
		return formatToolResult(strings.TrimSpace(string(b)))
	}
	return formatToolResult(string(compact))
}

func toolIcon(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "skill_load":
		return "📥"
	case "set_output_schema":
		return "🧩"
	case "skill_run":
		return "▶️"
	default:
		return "🔧"
	}
}
