//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main verifies that compacted tool-result placeholders can be
// recovered through session_load even when context compaction and token
// tailoring are both enabled.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	openaigo "github.com/openai/openai-go"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	appName           = "context-compaction-recovery-demo"
	agentName         = "recovery-agent"
	userID            = "demo-user"
	sessionID         = "demo-session"
	largeToolName     = "emit_large_result"
	headSentinel      = "REQ3_HEAD_SENTINEL_A9F1"
	tailSentinel      = "REQ3_TAIL_SENTINEL_Z7K3"
	repeatedBlock     = "REQ3_PAYLOAD_BLOCK_"
	tailorHeadMarker  = "REQ3_TAILOR_HEAD_MARKER"
	tailorMidMarker   = "REQ3_TAILOR_MIDDLE_MARKER"
	tailorTailMarker  = "REQ3_TAILOR_TAIL_MARKER"
	tailContentOffset = 24000
	tailContentLimit  = 128
)

var (
	modelName        = flag.String("model", "gpt-4o-mini", "OpenAI-compatible model name")
	maxInputTokens   = flag.Int("max-input-tokens", 1800, "Max input tokens for token tailoring")
	payloadBytes     = flag.Int("payload-bytes", 32000, "Approximate large tool-result byte size")
	fillerMessages   = flag.Int("filler-messages", 80, "Synthetic historical messages used to force token tailoring")
	dumpRequestsDir  = flag.String("dump-requests-dir", "", "Optional directory for captured model request JSON")
	compactionTokens = flag.Int("tool-result-max-tokens", 80, "Historical tool-result compaction threshold")
	oversizedTokens  = flag.Int("oversized-tool-result-max-tokens", 120, "Current-request head/tail truncation threshold")
	timeout          = flag.Duration("timeout", 90*time.Second, "End-to-end test timeout")
)

type requestCapture struct {
	mu   sync.Mutex
	raws []string
}

func (c *requestCapture) add(req *openaigo.ChatCompletionNewParams) {
	raw, err := json.Marshal(req)
	if err != nil {
		raw = []byte(fmt.Sprintf(`{"marshal_error":%q}`, err.Error()))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.raws = append(c.raws, string(raw))
	if *dumpRequestsDir != "" {
		name := fmt.Sprintf("%s/request-%02d.json", strings.TrimRight(*dumpRequestsDir, "/"), len(c.raws)-1)
		_ = os.WriteFile(name, raw, 0o644)
	}
}

func (c *requestCapture) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.raws...)
}

type emitLargeResultInput struct {
	Label string `json:"label,omitempty" jsonschema:"description=Optional label for the payload."`
}

type emitLargeResultOutput struct {
	Label   string `json:"label"`
	Content string `json:"content"`
}

func main() {
	flag.Parse()
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "FAILED: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	capture := &requestCapture{}
	r, sessionService := buildRunner(capture)
	defer r.Close()
	if err := seedTailoringHistory(ctx, sessionService); err != nil {
		return fmt.Errorf("seed tailoring history: %w", err)
	}

	firstPrompt := "Call emit_large_result exactly once with label=req3. " +
		"After the tool returns, reply with one short sentence. Do not copy the payload."
	if err := runTurn(ctx, r, firstPrompt); err != nil {
		return fmt.Errorf("first turn: %w", err)
	}

	toolEvent, err := findLargeToolResultEvent(ctx, sessionService)
	if err != nil {
		return err
	}
	if err := verifyOriginalToolResult(sessionService, toolEvent.ID); err != nil {
		return err
	}

	secondPrompt := fmt.Sprintf(
		"Recover the prior large tool result. You must call session_load using the compacted placeholder's event_id and tool_call_id. "+
			"Set content_offset=%d and content_limit=%d. "+
			"Use the event_id/tool_call_id from the placeholder only; do not use sentinel text as an identifier. "+
			"After loading, answer with the recovered tail sentinel string and nothing else.",
		tailContentOffset,
		tailContentLimit,
	)
	if err := runTurn(ctx, r, secondPrompt); err != nil {
		return fmt.Errorf("second turn: %w", err)
	}

	if err := verifySessionLoadWasUsed(ctx, sessionService); err != nil {
		return err
	}
	if err := verifyCapturedRequests(capture.snapshot()); err != nil {
		return err
	}

	fmt.Println("PASS: recovered compacted tool result with context compaction and token tailoring enabled")
	fmt.Printf("tool_result_event_id=%s\n", toolEvent.ID)
	fmt.Printf("tool_call_id=%s\n", toolEvent.Response.Choices[0].Message.ToolID)
	fmt.Printf("captured_model_requests=%d\n", len(capture.snapshot()))
	return nil
}

func buildRunner(capture *requestCapture) (runner.Runner, *sessioninmemory.SessionService) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	modelOpts := []openai.Option{
		openai.WithEnableTokenTailoring(true),
		openai.WithMaxInputTokens(*maxInputTokens),
		openai.WithTokenCounter(model.NewSimpleTokenCounter()),
		openai.WithChatRequestCallback(func(_ context.Context, req *openaigo.ChatCompletionNewParams) {
			capture.add(req)
		}),
	}
	if apiKey != "" {
		modelOpts = append(modelOpts, openai.WithAPIKey(apiKey))
	}
	if baseURL != "" {
		modelOpts = append(modelOpts, openai.WithBaseURL(baseURL))
	}
	llm := openai.New(*modelName, modelOpts...)

	temperature := 0.0
	maxTokens := 512
	agentInstance := llmagent.New(
		agentName,
		llmagent.WithModel(llm),
		llmagent.WithInstruction(strings.Join([]string{
			"You are testing session recovery.",
			"When the user asks to recover a prior tool result, call session_load before answering.",
			"Never guess sentinel strings from the prompt if a recovery tool is available.",
		}, "\n")),
		llmagent.WithTools([]tool.Tool{largeResultTool()}),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      false,
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
		}),
		llmagent.WithEnableOnDemandSession(true),
		llmagent.WithEnableContextCompaction(true),
		llmagent.WithContextCompactionKeepRecentRequests(0),
		llmagent.WithContextCompactionToolResultMaxTokens(*compactionTokens),
		llmagent.WithContextCompactionOversizedToolResultMaxTokens(*oversizedTokens),
		llmagent.WithContextCompactionTokenCounter(model.NewSimpleTokenCounter()),
		llmagent.WithMaxLLMCalls(4),
		llmagent.WithMaxToolIterations(3),
	)

	sessionService := sessioninmemory.NewSessionService()
	return runner.NewRunner(
		appName,
		agentInstance,
		runner.WithSessionService(sessionService),
	), sessionService
}

func seedTailoringHistory(
	ctx context.Context,
	sessionService *sessioninmemory.SessionService,
) error {
	if *fillerMessages <= 0 {
		return nil
	}
	key := session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}
	sess, err := sessionService.CreateSession(ctx, key, session.StateMap{})
	if err != nil {
		return err
	}
	for i := 1; i <= *fillerMessages; i++ {
		marker := ""
		switch i {
		case 1:
			marker = tailorHeadMarker
		case *fillerMessages / 2:
			marker = tailorMidMarker
		case *fillerMessages:
			marker = tailorTailMarker
		}
		content := fmt.Sprintf(
			"synthetic history %03d %s %s",
			i,
			marker,
			strings.Repeat("lorem ipsum ", 80),
		)
		evt := event.NewResponseEvent(
			"seed-invocation",
			"user",
			&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Index:   0,
					Message: model.NewUserMessage(content),
				}},
			},
		)
		evt.RequestID = fmt.Sprintf("seed-request-%03d", i)
		if err := sessionService.AppendEvent(ctx, sess, evt); err != nil {
			return err
		}
		reply := event.NewResponseEvent(
			"seed-invocation",
			agentName,
			&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.NewAssistantMessage(
						fmt.Sprintf("ack synthetic history %03d", i),
					),
				}},
			},
		)
		reply.RequestID = fmt.Sprintf("seed-reply-%03d", i)
		if err := sessionService.AppendEvent(ctx, sess, reply); err != nil {
			return err
		}
	}
	return nil
}

func largeResultTool() tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, req *emitLargeResultInput) (*emitLargeResultOutput, error) {
			label := "default"
			if req != nil && strings.TrimSpace(req.Label) != "" {
				label = strings.TrimSpace(req.Label)
			}
			return &emitLargeResultOutput{
				Label:   label,
				Content: buildLargePayload(*payloadBytes),
			}, nil
		},
		function.WithName(largeToolName),
		function.WithDescription("Emit one deterministic large payload containing recovery sentinels."),
	)
}

func buildLargePayload(targetBytes int) string {
	if targetBytes < tailContentOffset+4096 {
		targetBytes = tailContentOffset + 4096
	}
	var b strings.Builder
	b.WriteString(headSentinel)
	b.WriteString("\n")
	for b.Len() < tailContentOffset {
		fmt.Fprintf(&b, "%s%05d ", repeatedBlock, b.Len())
	}
	b.WriteString("\n")
	b.WriteString(tailSentinel)
	b.WriteString("\n")
	for b.Len() < targetBytes {
		fmt.Fprintf(&b, "%s%05d ", repeatedBlock, b.Len())
	}
	return b.String()
}

func runTurn(ctx context.Context, r runner.Runner, prompt string) error {
	ch, err := r.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(prompt),
		agent.WithRequestID(fmt.Sprintf("req-%d", time.Now().UnixNano())),
	)
	if err != nil {
		return err
	}
	for evt := range ch {
		if evt == nil || evt.Response == nil || evt.Response.Error == nil {
			continue
		}
		return errors.New(evt.Response.Error.Message)
	}
	return nil
}

func findLargeToolResultEvent(
	ctx context.Context,
	sessionService session.Service,
) (event.Event, error) {
	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		return event.Event{}, err
	}
	if sess == nil {
		return event.Event{}, errors.New("session not found")
	}
	for i := len(sess.Events) - 1; i >= 0; i-- {
		evt := sess.Events[i]
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			if msg.Role == model.RoleTool && msg.ToolName == largeToolName {
				return evt, nil
			}
		}
	}
	return event.Event{}, errors.New("large tool result event not found")
}

func verifyOriginalToolResult(
	sessionService session.WindowService,
	eventID string,
) error {
	window, err := sessionService.GetEventWindow(
		context.Background(),
		session.EventWindowRequest{
			Key: session.Key{
				AppName:   appName,
				UserID:    userID,
				SessionID: sessionID,
			},
			AnchorEventID: eventID,
			Roles:         []model.Role{model.RoleTool},
		},
	)
	if err != nil {
		return err
	}
	if window == nil || len(window.Entries) == 0 {
		return errors.New("direct event window did not return tool result")
	}
	content := window.Entries[0].Event.Response.Choices[0].Message.Content
	for _, want := range []string{headSentinel, tailSentinel, repeatedBlock} {
		if !strings.Contains(content, want) {
			return fmt.Errorf("original tool result missing %s", want)
		}
	}
	if strings.Contains(content, "characters truncated") ||
		strings.Contains(content, "Historical tool result omitted") {
		return errors.New("original session event was unexpectedly compacted")
	}
	return nil
}

func verifySessionLoadWasUsed(
	ctx context.Context,
	sessionService session.Service,
) error {
	sess, err := sessionService.GetSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		return err
	}
	if sess == nil {
		return errors.New("session not found")
	}
	for _, evt := range sess.Events {
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			if msg.Role != model.RoleTool || msg.ToolName != "session_load" {
				continue
			}
			if !strings.Contains(msg.Content, tailSentinel) {
				return errors.New("session_load ran but did not return tail sentinel")
			}
			if strings.Count(msg.Content, "characters truncated") > 0 {
				return errors.New("session_load result was unexpectedly truncated")
			}
			return nil
		}
	}
	return errors.New("model did not call session_load")
}

func verifyCapturedRequests(raws []string) error {
	if len(raws) < 4 {
		return fmt.Errorf("expected at least 4 model requests, got %d", len(raws))
	}
	all := strings.Join(raws, "\n")
	if *fillerMessages > 0 {
		firstReq := raws[0]
		if !strings.Contains(firstReq, tailorHeadMarker) ||
			!strings.Contains(firstReq, tailorTailMarker) {
			return errors.New("token tailoring did not preserve expected head/tail filler markers")
		}
		if strings.Contains(firstReq, tailorMidMarker) {
			return errors.New("token tailoring did not remove middle filler marker")
		}
	}
	if !strings.Contains(all, "Historical tool result omitted to save context") {
		return errors.New("captured requests never contained historical compaction placeholder")
	}
	if !strings.Contains(all, "event_id") || !strings.Contains(all, "tool_call_id") {
		return errors.New("captured placeholder did not expose recovery references")
	}
	if strings.Contains(raws[len(raws)-2], repeatedBlock) {
		return errors.New("pre-recovery model request still contained raw large payload")
	}
	finalReq := raws[len(raws)-1]
	if !strings.Contains(finalReq, tailSentinel) {
		return fmt.Errorf(
			"post-session_load request did not contain recovered tail sentinel; captured=%d contains_by_request=%v",
			len(raws),
			containsByRequest(raws, tailSentinel),
		)
	}
	if strings.Count(finalReq, "characters truncated from tool result") > 1 {
		return errors.New("post-session_load request appears to contain nested truncation markers")
	}
	if strings.Contains(finalReq, repeatedBlock+repeatedBlock) {
		return errors.New("post-session_load request appears to contain unsliced raw payload")
	}
	return nil
}

func containsByRequest(raws []string, needle string) []bool {
	found := make([]bool, len(raws))
	for i, raw := range raws {
		found[i] = strings.Contains(raw, needle)
	}
	return found
}
