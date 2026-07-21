//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package fakemodel provides deterministic model behavior for offline review
// runs.
package fakemodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const modelName = "fake-model"

const (
	skillLoadTool     = "skill_load"
	workspaceExecTool = "workspace_exec"
	submitResultsTool = "submit_review_results"
)

type scenario struct {
	fixture               string
	submission            submission
	retrySubmission       *submission
	expectsSandboxFailure bool
}

type submission struct {
	Findings         []reviewResult `json:"findings,omitempty"`
	Warnings         []reviewResult `json:"warnings,omitempty"`
	NeedsHumanReview []reviewResult `json:"needs_human_review,omitempty"`
	Conclusion       string         `json:"conclusion,omitempty"`
}

type reviewResult struct {
	Severity       string  `json:"severity"`
	Category       string  `json:"category"`
	File           string  `json:"file"`
	Line           int     `json:"line"`
	Title          string  `json:"title"`
	Evidence       string  `json:"evidence"`
	Recommendation string  `json:"recommendation,omitempty"`
	Confidence     float64 `json:"confidence"`
	Source         string  `json:"source"`
	RuleID         string  `json:"rule_id"`
}

var scenarios = map[string]scenario{
	"acceptance-clean": {
		fixture:    "acceptance-clean",
		submission: submission{Conclusion: "No actionable issues found. Go checks completed successfully."},
	},
	"acceptance-context-leak": findingScenario("acceptance-context-leak", reviewResult{
		Severity: "high", Category: "concurrency", File: "worker.go", Line: 15,
		Title:          "Goroutine ignores context cancellation",
		Evidence:       "The new goroutine ranges only over jobs and no longer selects on ctx.Done().",
		Recommendation: "Select on both jobs and ctx.Done() inside the goroutine.",
		Confidence:     0.99, Source: "agent", RuleID: "GO-CONC-001",
	}),
	"acceptance-database-lifecycle": findingScenario("acceptance-database-lifecycle", reviewResult{
		Severity: "high", Category: "database_lifecycle", File: "database.go", Line: 17,
		Title:          "Query rows are not closed",
		Evidence:       "db.QueryContext returns rows, but the function returns rows.Err() without closing rows.",
		Recommendation: "Defer rows.Close() immediately after the successful query and handle its errors as appropriate.",
		Confidence:     0.99, Source: "agent", RuleID: "GO-DB-001",
	}),
	"acceptance-duplicate-finding": duplicateFindingScenario(),
	"acceptance-missing-tests": {
		fixture: "acceptance-missing-tests",
		submission: submission{
			Warnings: []reviewResult{{
				Severity: "low", Category: "tests", File: "discount.go", Line: 13,
				Title:          "New discount branch has no matching test",
				Evidence:       "The diff adds behavior for totals over 100 without changing or adding a test file.",
				Recommendation: "Add boundary tests for totals at, below, and above 100.",
				Confidence:     0.88, Source: "agent", RuleID: "GO-TEST-001",
			}},
			Conclusion: "No high-confidence defect found; one test coverage warning remains.",
		},
	},
	"acceptance-resource-leak": findingScenario("acceptance-resource-leak", reviewResult{
		Severity: "high", Category: "resource_lifecycle", File: "file.go", Line: 18,
		Title:          "Opened file is not closed",
		Evidence:       "os.Open succeeds and io.ReadAll returns without any file.Close call.",
		Recommendation: "Defer file.Close() immediately after the successful open.",
		Confidence:     0.99, Source: "agent", RuleID: "GO-RES-001",
	}),
	"acceptance-sandbox-failure": {
		fixture:               "acceptance-sandbox-failure",
		expectsSandboxFailure: true,
		submission:            submission{Conclusion: "No code finding submitted; deterministic Go checks failed and the failure is recorded in sandbox evidence."},
	},
	"acceptance-secret-redaction": findingScenario("acceptance-secret-redaction", reviewResult{
		Severity: "critical", Category: "sensitive_info", File: "config.go", Line: 14,
		Title:          "Hard-coded API credential",
		Evidence:       "The added APIKey constant contains an OpenAI-shaped credential; the plaintext value is redacted.",
		Recommendation: "Remove the credential from source and load it from a secret manager or environment variable.",
		Confidence:     0.99, Source: "agent", RuleID: "GO-SECRET-001",
	}),
	"acceptance-security": findingScenario("acceptance-security", reviewResult{
		Severity: "critical", Category: "security", File: "command.go", Line: 15,
		Title:          "User input is executed by a shell",
		Evidence:       "The changed code passes input to exec.Command(\"sh\", \"-c\", input), enabling command injection.",
		Recommendation: "Avoid a shell and pass validated arguments directly to a fixed executable.",
		Confidence:     0.99, Source: "agent", RuleID: "GO-SEC-001",
	}),
}

// FakeModel is one task-scoped deterministic model for a registered fixture
// scenario. It follows the same multi-turn tool protocol as a provider model.
type FakeModel struct {
	scenario scenario
}

// NewForFixture creates the deterministic model scenario registered for a
// review fixture.
func NewForFixture(fixture string) (fake *FakeModel, err error) {
	configured, ok := scenarios[fixture]
	if !ok {
		return nil, fmt.Errorf("fake model fixture %q is not registered", fixture)
	}
	return &FakeModel{scenario: configured}, nil
}

// GenerateContent derives its next action from the framework-provided message
// history. Fixture results are compiled into this model and never read from
// expectations.json or the prepared workspace.
func (f *FakeModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	if f == nil {
		return nil, errors.New("fake model is nil")
	}
	if request == nil {
		return nil, errors.New("fake model request is nil")
	}
	if !hasUserMessage(request.Messages) {
		return nil, errors.New("fake model request requires user input")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	lastTool := lastToolMessage(request.Messages)
	var response *model.Response
	switch {
	case lastTool == nil:
		response = f.toolCall(request, skillLoadTool, map[string]any{
			"skill": "code-review",
			"docs":  []string{"references/rules.md"},
		})
	case lastTool.ToolName == skillLoadTool:
		response = f.toolCall(request, workspaceExecTool, map[string]any{
			"command":     "sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
			"timeout_sec": 120,
		})
	case lastTool.ToolName == workspaceExecTool:
		submission := f.scenario.submission
		failed := workspaceFailed(lastTool.Content)
		if failed != f.scenario.expectsSandboxFailure {
			submission.NeedsHumanReview = append(submission.NeedsHumanReview, reviewResult{
				Severity: "medium", Category: "correctness", File: "", Line: 0,
				Title:          "Go checks returned an unexpected status",
				Evidence:       "The workspace_exec result did not match the fixture's expected success state.",
				Recommendation: "Inspect the recorded sandbox output before relying on the review conclusion.",
				Confidence:     1, Source: "sandbox", RuleID: "GO-COR-001",
			})
			submission.Conclusion += " The sandbox status was unexpected and requires human review."
		}
		response = f.toolCall(request, submitResultsTool, submission)
	case lastTool.ToolName == submitResultsTool:
		switch {
		case submissionAccepted(lastTool.Content):
			response = f.completion()
		case f.scenario.retrySubmission != nil &&
			toolMessageCount(request.Messages, submitResultsTool) == 1:
			response = f.toolCall(
				request,
				submitResultsTool,
				*f.scenario.retrySubmission,
			)
		default:
			response = errorResponse(
				f.scenario.fixture,
				"submit_review_results was rejected and no valid retry remains",
			)
		}
	default:
		return nil, fmt.Errorf("fake model cannot continue after tool %q", lastTool.ToolName)
	}

	ch := make(chan *model.Response, 1)
	ch <- response
	close(ch)
	return ch, nil
}

// Info returns the stable provider-independent name used by fake-model runs.
func (f *FakeModel) Info() model.Info {
	return model.Info{
		Name: modelName,
	}
}

func findingScenario(fixture string, finding reviewResult) scenario {
	return scenario{
		fixture: fixture,
		submission: submission{
			Findings:   []reviewResult{finding},
			Conclusion: "One actionable issue found. Go checks completed successfully.",
		},
	}
}

func duplicateFindingScenario() scenario {
	findings := []reviewResult{
		{
			Severity: "high", Category: "resource_lifecycle", File: "http.go", Line: 19,
			Title:          "HTTP response body is not closed",
			Evidence:       "The response returned by http.Get leaves response.Body open on the successful path.",
			Recommendation: "Defer response.Body.Close() after checking the error.",
			Confidence:     0.99, Source: "agent", RuleID: "GO-RES-001",
		},
		{
			Severity: "medium", Category: "resource_lifecycle", File: "http.go", Line: 19,
			Title:          "Leaked HTTP response body",
			Evidence:       "The changed success path returns without closing response.Body.",
			Recommendation: "Restore a deferred response.Body.Close().",
			Confidence:     0.96, Source: "skill", RuleID: "GO-RES-001",
		},
	}
	corrected := submission{
		Findings:   findings,
		Conclusion: "One actionable resource lifecycle issue found. Go checks completed successfully.",
	}
	conflicting := corrected
	conflicting.Warnings = []reviewResult{{
		Severity: "medium", Category: "resource_lifecycle", File: "http.go", Line: 19,
		Title:          "Response body cleanup needs review",
		Evidence:       "The same response body observation was routed as a warning.",
		Recommendation: "Choose one result collection for this rule and location.",
		Confidence:     0.75, Source: "agent", RuleID: "GO-RES-001",
	}}
	return scenario{
		fixture:         "acceptance-duplicate-finding",
		submission:      conflicting,
		retrySubmission: &corrected,
	}
}

func (f *FakeModel) toolCall(request *model.Request, name string, arguments any) *model.Response {
	if _, ok := request.Tools[name]; !ok {
		available := make([]string, 0, len(request.Tools))
		for toolName := range request.Tools {
			available = append(available, toolName)
		}
		sort.Strings(available)
		return errorResponse(f.scenario.fixture, fmt.Sprintf("required tool %q is unavailable; available tools: %s", name, strings.Join(available, ", ")))
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return errorResponse(f.scenario.fixture, fmt.Sprintf("encode %s arguments: %v", name, err))
	}

	var (
		finishReason = "tool_calls"
		reason       string
	)

	if name == workspaceExecTool {
		reason = "I need to run the code-review Skill's repository checks in the configured sandbox to verify whether go test and go vet pass or fail for the affected module."
	}
	return &model.Response{
		ID:     "fake-model-" + f.scenario.fixture + "-" + name,
		Object: model.ObjectTypeChatCompletion, Model: modelName, Done: true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{Role: model.RoleAssistant, Content: reason, ToolCalls: []model.ToolCall{{
				ID:       "fake-" + f.scenario.fixture + "-" + name,
				Type:     "function",
				Function: model.FunctionDefinitionParam{Name: name, Arguments: encoded},
			}}},
			FinishReason: &finishReason,
		}},
	}
}

func (f *FakeModel) completion() *model.Response {
	finishReason := "stop"
	return &model.Response{
		ID:     "fake-model-" + f.scenario.fixture + "-complete",
		Object: model.ObjectTypeChatCompletion, Model: modelName, Done: true,
		Choices: []model.Choice{{
			Index:        0,
			Message:      model.NewAssistantMessage("Review results submitted for " + f.scenario.fixture + "."),
			FinishReason: &finishReason,
		}},
	}
}

func errorResponse(fixture, message string) *model.Response {
	return &model.Response{
		ID: "fake-model-" + fixture + "-error", Object: model.ObjectTypeError,
		Model: modelName, Done: true,
		Error: &model.ResponseError{Type: model.ErrorTypeFlowError, Message: message},
	}
}

func hasUserMessage(messages []model.Message) bool {
	for _, message := range messages {
		if message.Role == model.RoleUser {
			return true
		}
	}
	return false
}

func lastToolMessage(messages []model.Message) *model.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleTool {
			return &messages[i]
		}
	}
	return nil
}

func toolMessageCount(messages []model.Message, toolName string) int {
	count := 0
	for _, message := range messages {
		if message.Role == model.RoleTool && message.ToolName == toolName {
			count++
		}
	}
	return count
}

func submissionAccepted(content string) bool {
	var result struct {
		Status string `json:"status"`
	}
	return json.Unmarshal([]byte(content), &result) == nil &&
		result.Status == "accepted"
}

func workspaceFailed(content string) bool {
	var result struct {
		Status   string `json:"status"`
		ExitCode *int   `json:"exit_code"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return true
	}
	if result.Error != "" || strings.EqualFold(result.Status, "error") || strings.EqualFold(result.Status, "failed") {
		return true
	}
	return result.ExitCode != nil && *result.ExitCode != 0
}
