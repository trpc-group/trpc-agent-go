//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main compares two agent setups on the same repository questions:
// one agent uses the local code_search tool, and the other uses Augment code
// search through MCP. Each run records the final answer and intermediate tool
// calls/results to a markdown report.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	defaultOutputDir      = "output/code_context_engine"
	repoURL               = "https://github.com/trpc-group/trpc-agent-go"
	repoName              = "trpc-agent-go"
	repoBranch            = "main"
	augmentServerURL      = "https://api.augmentcode.com/mcp"
	augmentToolName       = "augment_code_search"
	augmentAuthHeader     = "Authorization"
	defaultModelName      = "gpt-5.4"
	localUserID           = "code-context-local-user"
	augmentUserID         = "code-context-augment-user"
	defaultLocalMCPURL    = "http://localhost:3001/mcp"
	defaultLocalMCPToolNm = "code_search"
)

// Run modes selectable via the -mode flag.
const (
	runModeBoth    = "both"
	runModeLocal   = "local"
	runModeAugment = "augment"
)

var (
	runMode          = flag.String("mode", runModeBoth, "Which agent(s) to run: local | augment | both")
	outputDir        = flag.String("output-dir", defaultOutputDir, "Directory where per-case markdown reports are written")
	localMCPURL      = flag.String("local-mcp-url", defaultLocalMCPURL, "URL of our own code-search MCP server used by the local agent")
	localMCPToolName = flag.String("local-mcp-tool", defaultLocalMCPToolNm, "Name of the MCP tool exposed by our local code-search MCP server")
)

type comparisonCase struct {
	Name        string
	Description string
	Prompt      string
}

type toolCallTrace struct {
	CallID    string `json:"call_id"`
	ToolName  string `json:"tool_name"`
	Arguments string `json:"arguments"`
}

type toolResultTrace struct {
	CallID   string `json:"call_id"`
	ToolName string `json:"tool_name"`
	Content  string `json:"content"`
}

type agentRunResult struct {
	FinalAnswer string            `json:"final_answer"`
	ToolCalls   []toolCallTrace   `json:"tool_calls,omitempty"`
	ToolResults []toolResultTrace `json:"tool_results,omitempty"`
}

func main() {
	flag.Parse()
	ctx := context.Background()

	mode := strings.ToLower(strings.TrimSpace(*runMode))
	switch mode {
	case runModeBoth, runModeLocal, runModeAugment:
	default:
		log.Fatalf("invalid -mode %q: expected one of local|augment|both", *runMode)
	}
	runLocal := mode == runModeBoth || mode == runModeLocal
	runAugment := mode == runModeBoth || mode == runModeAugment

	reportDir := strings.TrimSpace(*outputDir)
	if reportDir == "" {
		reportDir = defaultOutputDir
	}
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		log.Fatalf("failed to create comparison output dir %q: %v", reportDir, err)
	}
	modelName := strings.TrimSpace(util.GetEnvOrDefault("MODEL_NAME", defaultModelName))

	fmt.Println("Code Context Engine Agent Comparison")
	fmt.Println("====================================")
	fmt.Printf("Repository URL: %s\n", repoURL)
	fmt.Printf("Repository Name: %s\n", repoName)
	fmt.Printf("Repository Branch: %s\n", repoBranch)
	fmt.Printf("Agent Model: %s\n", modelName)
	fmt.Printf("Local MCP URL: %s\n", *localMCPURL)
	fmt.Printf("Local MCP Tool: %s\n", *localMCPToolName)
	fmt.Printf("Run Mode: %s\n", mode)
	fmt.Printf("Output Dir: %s\n", reportDir)

	var localAgent *localCodeSearchAgentRunner
	if runLocal {
		la, err := newLocalCodeSearchAgentRunner(localAgentConfig{
			ModelName:    modelName,
			MCPServerURL: *localMCPURL,
			MCPToolName:  *localMCPToolName,
		})
		if err != nil {
			log.Fatalf("create local agent runner failed: %v", err)
		}
		localAgent = la
		defer func() {
			if err := localAgent.Close(); err != nil {
				log.Printf("close local agent runner failed: %v", err)
			}
		}()
	}

	var (
		augmentAgent   *augmentCodeAgentRunner
		augmentInitErr error
	)
	if runAugment {
		augmentAgent, augmentInitErr = newAugmentCodeAgentRunner(modelName)
		if augmentInitErr != nil {
			log.Printf("create augment agent runner failed: %v", augmentInitErr)
		} else {
			defer func() {
				if err := augmentAgent.Close(); err != nil {
					log.Printf("close augment agent runner failed: %v", err)
				}
			}()
		}
	}

	cases := defaultCases()
	fmt.Println("\nStep 2: Per-question agent comparison")
	fmt.Println("------------------------------------")
	for _, c := range cases {
		printCaseHeader(c)

		var (
			localResult   *agentRunResult
			localErr      error
			augmentResult *agentRunResult
			augmentErr    error
		)

		if runLocal {
			localResult, localErr = localAgent.RunCase(ctx, c)
			printAgentSummary("Local agent + code_search", localResult, localErr)
		}

		if runAugment {
			if augmentAgent != nil {
				augmentResult, augmentErr = augmentAgent.RunCase(ctx, c)
				printAgentSummary("Augment agent + MCP", augmentResult, augmentErr)
			} else {
				augmentErr = fmt.Errorf("augment agent runner not initialized: %w", augmentInitErr)
				printAgentSummary("Augment agent + MCP", nil, augmentErr)
			}
		}

		reportPath, err := writeCaseReport(reportDir, mode, c, localResult, localErr, augmentResult, augmentErr)
		if err != nil {
			log.Printf("write comparison report for case %s failed: %v", c.Name, err)
			continue
		}
		fmt.Printf("Report written: %s\n", reportPath)
	}
}

func newAgentModel(modelName string) model.Model {
	return openaimodel.New(modelName)
}

func defaultCases() []comparisonCase {
	return []comparisonCase{
		{
			Name:        "multi_agent_session_isolation",
			Description: "Ask how sub-agents share or isolate session state / message history in a multi-agent setup.",
			Prompt:      "In trpc-agent-go's multi-agent system, when a coordinator LLMAgent calls a sub-agent via AgentTool or via WithSubAgents, how is session state isolated between them? Please explain: (1) whether they share the same session by default, (2) what the MessageFilterMode values (FullContext / RequestContext / IsolatedRequest / IsolatedInvocation) mean and when each should be used, (3) how GraphAgent's WithSubgraphIsolatedMessages differs from LLMAgent.WithMessageFilterMode(IsolatedInvocation) especially for multi-turn tool calling, and (4) how skill state keys are scoped between coordinator and sub-agent. Cite exact function / option names and file paths.",
		},
	}
}

func runAgentInvocation(
	ctx context.Context,
	r runner.Runner,
	userID string,
	sessionID string,
	prompt string,
) (*agentRunResult, error) {
	eventCh, err := r.Run(ctx, userID, sessionID, model.NewUserMessage(prompt))
	if err != nil {
		return nil, fmt.Errorf("run failed: %w", err)
	}

	result := &agentRunResult{}
	seenToolCalls := map[string]int{}
	seenToolResults := map[string]int{}
	for evt := range eventCh {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return result, fmt.Errorf("runner event error: %s", evt.Error.Message)
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		for _, choice := range evt.Response.Choices {
			for _, toolCall := range choice.Message.ToolCalls {
				trace := toolCallTrace{
					CallID:    toolCall.ID,
					ToolName:  toolCall.Function.Name,
					Arguments: string(toolCall.Function.Arguments),
				}
				if idx, ok := seenToolCalls[toolCall.ID]; ok && toolCall.ID != "" {
					result.ToolCalls[idx] = trace
					continue
				}
				if toolCall.ID != "" {
					seenToolCalls[toolCall.ID] = len(result.ToolCalls)
				}
				result.ToolCalls = append(result.ToolCalls, trace)
			}
			if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
				trace := toolResultTrace{
					CallID:   choice.Message.ToolID,
					ToolName: choice.Message.ToolName,
					Content:  choice.Message.Content,
				}
				if idx, ok := seenToolResults[choice.Message.ToolID]; ok {
					result.ToolResults[idx] = trace
				} else {
					seenToolResults[choice.Message.ToolID] = len(result.ToolResults)
					result.ToolResults = append(result.ToolResults, trace)
				}
			}
			if evt.IsFinalResponse() && choice.Message.Role == model.RoleAssistant && strings.TrimSpace(choice.Message.Content) != "" {
				result.FinalAnswer = strings.TrimSpace(choice.Message.Content)
			}
		}
	}
	return result, nil
}

func printCaseHeader(c comparisonCase) {
	fmt.Println("\n" + strings.Repeat("-", 72))
	fmt.Printf("CASE :: %s\n", c.Name)
	fmt.Println(strings.Repeat("-", 72))
	fmt.Printf("Description: %s\n", c.Description)
	fmt.Printf("Prompt: %s\n", c.Prompt)
}

func printAgentSummary(label string, result *agentRunResult, err error) {
	fmt.Printf("\n[%s]\n", label)
	if err != nil {
		fmt.Printf("  error: %v\n", err)
		return
	}
	if result == nil {
		fmt.Println("  no result")
		return
	}
	fmt.Printf("  tool_calls: %d | tool_results: %d\n", len(result.ToolCalls), len(result.ToolResults))
	if result.FinalAnswer != "" {
		fmt.Printf("  final_answer: %s\n", compactText(result.FinalAnswer, 220))
	} else {
		fmt.Println("  final_answer: <empty>")
	}
	if len(result.ToolCalls) > 0 {
		fmt.Printf("  first_tool: %s\n", result.ToolCalls[0].ToolName)
	}
}

func writeCaseReport(
	dir string,
	mode string,
	c comparisonCase,
	localResult *agentRunResult,
	localErr error,
	augmentResult *agentRunResult,
	augmentErr error,
) (string, error) {
	var b strings.Builder
	b.WriteString("# Code Context Agent Comparison\n\n")
	b.WriteString("## Case\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n", c.Name))
	b.WriteString(fmt.Sprintf("- Description: %s\n", c.Description))
	b.WriteString(fmt.Sprintf("- Prompt: %s\n", c.Prompt))
	b.WriteString(fmt.Sprintf("- Run Mode: %s\n", mode))

	if mode == runModeBoth || mode == runModeLocal {
		b.WriteString("\n## Local Agent + code_search\n")
		b.WriteString(renderAgentReport(localResult, localErr))
	}

	if mode == runModeBoth || mode == runModeAugment {
		b.WriteString("\n## Augment Agent + MCP\n")
		b.WriteString(renderAgentReport(augmentResult, augmentErr))
	}

	path := filepath.Join(dir, c.Name+".md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func renderAgentReport(result *agentRunResult, err error) string {
	if err != nil && result == nil {
		return fmt.Sprintf("Error: %v\n", err)
	}
	if result == nil {
		return "Not executed.\n"
	}
	var b strings.Builder
	if err != nil {
		b.WriteString(fmt.Sprintf("> Error: %v\n\n", err))
	}
	b.WriteString("### Final Answer\n")
	if strings.TrimSpace(result.FinalAnswer) == "" {
		b.WriteString("<empty>\n")
	} else {
		b.WriteString("```text\n")
		b.WriteString(strings.TrimSpace(result.FinalAnswer))
		b.WriteString("\n```\n")
	}

	b.WriteString("\n### Tool Calls\n")
	if len(result.ToolCalls) == 0 {
		b.WriteString("No tool calls.\n")
	} else {
		for i, call := range result.ToolCalls {
			b.WriteString(fmt.Sprintf("#### [%d] %s\n", i+1, call.ToolName))
			b.WriteString(fmt.Sprintf("- Call ID: `%s`\n", call.CallID))
			b.WriteString("- Arguments:\n")
			b.WriteString("```json\n")
			b.WriteString(prettyToolPayload(call.Arguments))
			b.WriteString("\n```\n")
		}
	}

	b.WriteString("\n### Tool Results\n")
	if len(result.ToolResults) == 0 {
		b.WriteString("No tool results.\n")
	} else {
		for i, toolResult := range result.ToolResults {
			b.WriteString(fmt.Sprintf("#### [%d] %s\n", i+1, toolResult.ToolName))
			b.WriteString(fmt.Sprintf("- Call ID: `%s`\n", toolResult.CallID))
			b.WriteString("- Content:\n")
			b.WriteString("```json\n")
			b.WriteString(prettyToolPayload(toolResult.Content))
			b.WriteString("\n```\n")
		}
	}
	return b.String()
}

func compactText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

func prettyToolPayload(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "{}"
	}
	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err == nil {
		return prettyJSON(payload)
	}
	return trimmed
}

func prettyJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}
