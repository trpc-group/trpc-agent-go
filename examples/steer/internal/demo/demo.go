//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package demo contains shared configuration and checks for steering examples.
package demo

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const initial = "Q1: List 100 short software release checklist items, numbered 1 to 100. Start immediately with item 1."
const update = "Q2: Change the answer to a single short sentence about release testing."

type config struct {
	model                                                 string
	check, stream, persist, parallel, withdraw, interrupt bool
	calls                                                 int
	timeout                                               time.Duration
}

func defaultModel() string {
	for _, key := range []string{"OPENAI_MODEL", "MODEL_NAME"} {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return "gpt-4.1-mini"
}
func parse(args []string) (config, error) {
	c := config{}
	fs := flag.NewFlagSet("steer", flag.ContinueOnError)
	fs.StringVar(&c.model, "model", defaultModel(), "Model name")
	fs.BoolVar(&c.check, "check", false, "Verify the scenario and exit nonzero on failure")
	fs.BoolVar(&c.stream, "stream", true, "Use model streaming")
	fs.BoolVar(&c.persist, "persist-partial", false, "Persist interrupted assistant text")
	fs.BoolVar(&c.parallel, "parallel", false, "Execute tool calls concurrently")
	fs.BoolVar(&c.withdraw, "withdraw", false, "Withdraw an extra queued message before adding Q2")
	fs.BoolVar(&c.interrupt, "interrupt", true, "Enable interruption (false demonstrates the existing boundary)")
	fs.IntVar(&c.calls, "calls", 3, "Number of calls in the accepted batch: 1 or 3")
	fs.DurationVar(&c.timeout, "timeout", 120*time.Second, "Maximum scenario duration")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if c.calls != 1 && c.calls != 3 {
		return c, errors.New("calls must be 1 or 3")
	}
	if c.timeout <= 0 {
		return c, errors.New("timeout must be positive")
	}
	return c, nil
}

// Main runs one independently executable scenario.
func Main(scenario string) {
	c, err := parse(os.Args[1:])
	if err == nil {
		if os.Getenv("OPENAI_API_KEY") == "" {
			err = errors.New("OPENAI_API_KEY is not set")
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
			defer cancel()
			if scenario == "chat" {
				err = runChat(ctx, c, os.Stdin, os.Stdout)
			} else {
				err = run(ctx, scenario, c)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
}

type demo struct {
	config         config
	scenario       string
	runner         runner.Runner
	requestID      string
	mu             sync.Mutex
	requests       [][]model.Message
	counts         map[string]int
	firstErr       error
	enqueued       []string
	queuedAt       time.Time
	once           sync.Once
	transportCalls atomic.Int32
}

func (d *demo) fail(err error) {
	if err == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.firstErr == nil {
		d.firstErr = err
	}
}
func (d *demo) enqueue() {
	d.once.Do(func() {
		if d.config.withdraw {
			if err := runner.EnqueueUserMessage(d.runner, d.requestID, model.NewUserMessage("withdrawn input")); err != nil {
				d.fail(err)
				return
			}
			if !runner.CancelQueuedUserMessages(d.runner, d.requestID) {
				d.fail(errors.New("could not withdraw queued input"))
				return
			}
			fmt.Println("[queue] withdrew pending input")
		}
		messages := []string{update}
		if d.scenario == "multi_input" {
			messages = append(messages, "Q3: Mention automated tests.", "Q4: Use English.")
		}
		for _, text := range messages {
			if err := runner.EnqueueUserMessage(d.runner, d.requestID, model.NewUserMessage(text)); err != nil {
				d.fail(err)
				return
			}
			d.mu.Lock()
			d.enqueued = append(d.enqueued, text)
			if d.queuedAt.IsZero() {
				d.queuedAt = time.Now()
			}
			d.mu.Unlock()
			fmt.Printf("[queue] accepted %s\n", text)
		}
	})
}

type transport struct{ demo *demo }

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	n := t.demo.transportCalls.Add(1)
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		if info.Err != nil {
			return
		}
		if n == 1 && t.demo.scenario == "before_response" {
			t.demo.enqueue()
		}
		if n == 2 && t.demo.scenario == "cancel" {
			t.demo.runner.(runner.ManagedRunner).Cancel(t.demo.requestID)
			fmt.Println("[run] explicitly cancelled")
		}
	}}
	return http.DefaultTransport.RoundTrip(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
}

type workArgs struct {
	Job string `json:"job"`
}

func (d *demo) work(ctx context.Context, in workArgs) (string, error) {
	d.mu.Lock()
	d.counts[in.Job]++
	d.mu.Unlock()
	fmt.Printf("[tool] start %s\n", in.Job)
	if in.Job == "A" {
		d.enqueue()
	}
	// This delay represents ordinary work; input is triggered by tool entry.
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		d.fail(fmt.Errorf("tool %s cancelled: %w", in.Job, ctx.Err()))
		return "", ctx.Err()
	case <-timer.C:
	}
	fmt.Printf("[tool] end %s\n", in.Job)
	return "completed " + in.Job, nil
}

func run(ctx context.Context, scenario string, c config) error {
	started := time.Now()
	d := &demo{config: c, scenario: scenario, requestID: fmt.Sprintf("steer-%d", time.Now().UnixNano()), counts: map[string]int{}}
	callbacks := model.NewCallbacks()
	callbacks.RegisterBeforeModel(func(_ context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		d.mu.Lock()
		d.requests = append(d.requests, append([]model.Message(nil), args.Request.Messages...))
		n := len(d.requests)
		d.mu.Unlock()
		fmt.Printf("[model] request %d request_id=%s\n", n, d.requestID)
		if n > 1 {
			args.Request.Tools = nil
		}
		return nil, nil
	})
	isTool := scenario == "tool_batch" || scenario == "multi_input"
	question := initial
	instruction := "Follow the latest user instructions. Do not call tools unless asked."
	var tools []tool.Tool
	if isTool {
		jobs := "A, B, C"
		if c.calls == 1 {
			jobs = "A"
		}
		question = "Q1: Call work for jobs " + jobs + " in ONE assistant tool-call batch, then summarize all results."
		instruction = "Your first response must contain exactly one work tool call for EACH requested job, all in the SAME response. Do not call any job twice. After tool results, follow the newer user instructions and answer briefly."
		tools = []tool.Tool{function.NewFunctionTool(d.work, function.WithName("work"), function.WithDescription("Complete a local job. job must be A, B or C."))}
	}
	maxTokens := 8192
	ag := llmagent.New("steer-agent", llmagent.WithModel(openai.New(c.model, openai.WithHTTPClientOptions(openai.WithHTTPClientTransport(transport{d})))),
		llmagent.WithInstruction(instruction), llmagent.WithTools(tools), llmagent.WithModelCallbacks(callbacks), llmagent.WithInterruptOnUserMessage(c.interrupt),
		llmagent.WithEnableParallelTools(c.parallel), llmagent.WithMaxLLMCalls(4), llmagent.WithGenerationConfig(model.GenerationConfig{Stream: c.stream, MaxTokens: &maxTokens}))
	d.runner = runner.NewRunner("steer-examples", ag, runner.WithPersistInterruptedAssistant(c.persist))
	defer d.runner.Close()
	fmt.Printf("Scenario: %s Model: %s RequestID: %s\n", scenario, c.model, d.requestID)
	events, err := d.runner.Run(ctx, "demo-user", d.requestID, model.NewUserMessage(question), agent.WithRequestID(d.requestID))
	if err != nil {
		return err
	}
	markers, completions, consumed := 0, 0, []string{}
	oldID, invocationID := "", ""
	finalAnswer := false
	var interruptedAfter, consumedAfter time.Duration
	var interruptedText strings.Builder
	for evt := range events {
		if evt.Response == nil {
			continue
		}
		if invocationID == "" {
			invocationID = evt.InvocationID
		}
		if evt.InvocationID != invocationID || evt.RequestID != d.requestID {
			d.fail(errors.New("run identity changed"))
		}
		if evt.IsRunnerCompletion() {
			completions++
			fmt.Println("[run] completion")
			continue
		}
		if evt.Error != nil {
			d.fail(fmt.Errorf("model/run error: %s", evt.Error.Message))
			continue
		}
		if evt.Object == model.ObjectTypeModelInterrupted {
			markers++
			finalAnswer = false
			d.mu.Lock()
			if !d.queuedAt.IsZero() {
				interruptedAfter = time.Since(d.queuedAt)
			}
			d.mu.Unlock()
			oldID = evt.Response.ID
			fmt.Printf("[model] interrupted response_id=%s\n", oldID)
			continue
		}
		if oldID != "" && evt.Response.ID == oldID {
			d.fail(errors.New("old response appeared after interruption"))
		}
		for _, choice := range evt.Choices {
			if choice.Message.Role == model.RoleUser {
				consumed = append(consumed, choice.Message.Content)
				d.mu.Lock()
				if !d.queuedAt.IsZero() {
					consumedAfter = time.Since(d.queuedAt)
				}
				d.mu.Unlock()
				fmt.Printf("[queue] consumed %s\n", choice.Message.Content)
			}
			if choice.Message.Role == model.RoleTool {
				fmt.Printf("[tool] result %s\n", choice.Message.Content)
			}
			if !evt.IsPartial && len(choice.Message.ToolCalls) > 0 {
				fmt.Printf("[model] accepted %d tool calls\n", len(choice.Message.ToolCalls))
			}
			if !evt.IsPartial && choice.Message.Role == model.RoleAssistant {
				finish := ""
				if choice.FinishReason != nil {
					finish = *choice.FinishReason
				}
				fmt.Printf("[model] completed response_id=%s finish=%s text_bytes=%d tool_calls=%d\n", evt.Response.ID, finish, len(choice.Message.Content), len(choice.Message.ToolCalls))
			}
			if scenario == "cancel" && evt.IsPartial && choice.Delta.ReasoningContent != "" {
				fmt.Println("[model] received a reasoning delta")
				d.enqueue()
			}
			if evt.IsPartial && choice.Delta.Content != "" {
				if markers == 0 {
					interruptedText.WriteString(choice.Delta.Content)
				}
				fmt.Printf("[assistant] delta response_id=%s %s\n", evt.Response.ID, strings.TrimSpace(choice.Delta.Content))
				if scenario == "stream" || scenario == "cancel" {
					d.enqueue()
				}
			}
			if !evt.IsPartial && choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
				finalAnswer = true
				fmt.Printf("[assistant] %s\n", choice.Message.Content)
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.firstErr != nil {
		return d.firstErr
	}
	if c.check {
		if completions != 1 {
			return fmt.Errorf("expected one completion, got %d", completions)
		}
		if len(d.requests) != 2 {
			return fmt.Errorf("scenario not covered: expected two model requests, got %d", len(d.requests))
		}
		if len(d.enqueued) == 0 || !equalStrings(consumed, d.enqueued) {
			return errors.New("queued input was not consumed exactly once in FIFO order")
		}
		wantMarkers := 1
		if isTool || !c.interrupt {
			wantMarkers = 0
		}
		if markers != wantMarkers {
			return fmt.Errorf("scenario not covered: expected %d interruptions, got %d", wantMarkers, markers)
		}
		if scenario != "cancel" && !finalAnswer {
			return errors.New("missing final answer")
		}
		if scenario == "cancel" && d.transportCalls.Load() < 2 {
			return errors.New("second provider request was not sent before cancellation")
		}
		last := d.requests[1]
		users := []string{}
		results := 0
		for _, message := range last {
			if message.Role == model.RoleTool {
				results++
			}
			if message.Role == model.RoleUser {
				users = append(users, message.Content)
				if message.Content == update && isTool && results != c.calls {
					return errors.New("input appeared before all tool results")
				}
			}
		}
		expected := append([]string{question}, d.enqueued...)
		if !equalStrings(users, expected) {
			return fmt.Errorf("unexpected request history: user messages=%q", users)
		}
		if scenario == "stream" {
			found := false
			for _, message := range last {
				if message.Role == model.RoleAssistant && message.Content == interruptedText.String() && message.Content != "" {
					found = true
				}
			}
			if found != c.persist {
				return errors.New("interrupted text persistence did not match configuration")
			}
		}
		if isTool {
			for _, job := range []string{"A", "B", "C"} {
				want := 1
				if c.calls == 1 && job != "A" {
					want = 0
				}
				if d.counts[job] != want {
					return fmt.Errorf("scenario not covered: job %s ran %d times, expected %d", job, d.counts[job], want)
				}
			}
		}
		fmt.Println("PASS: scenario invariants verified")
	}
	summary := map[string]any{"scenario": scenario, "status": "PASS", "model_requests": len(d.requests), "interruptions": markers, "completions": completions, "duration_ms": time.Since(started).Milliseconds(), "checked": c.check, "enqueue_to_interrupt_ms": interruptedAfter.Milliseconds(), "enqueue_to_consumed_ms": consumedAfter.Milliseconds()}
	payload, _ := json.Marshal(summary)
	fmt.Println(string(payload))
	return nil
}
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
