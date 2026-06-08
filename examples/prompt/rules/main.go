package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// scenarioKey is used to pass a human-readable label through context.
type scenarioKey struct{}

// debugModel prints the final request messages it receives and returns a
// deterministic "OK" assistant response (so the injected context does not
// accidentally persist through assistant echoing).
type debugModel struct{}

func (m *debugModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	label, _ := ctx.Value(scenarioKey{}).(string)
	if label == "" {
		label = "model.request"
	}
	printRequest(label, req)

	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		ch <- &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{
				{Index: 0, Message: model.NewAssistantMessage("OK")},
			},
		}
	}()
	return ch, nil
}

func (m *debugModel) Info() model.Info {
	return model.Info{Name: "debug-model"}
}

func printRequest(label string, req *model.Request) {
	fmt.Printf("\n=== %s ===\n", label)
	if req == nil {
		fmt.Println("(nil request)")
		return
	}
	for i, msg := range req.Messages {
		fmt.Printf("%02d %-9s %s\n", i, msg.Role.String(), summarizeMessage(msg))
	}
}

func summarizeMessage(msg model.Message) string {
	// One-line view for readability.
	content := strings.ReplaceAll(msg.Content, "\n", `\n`)
	content = strings.TrimSpace(content)
	if content == "" && len(msg.ContentParts) > 0 {
		content = fmt.Sprintf("<%d content parts>", len(msg.ContentParts))
	}
	if content == "" {
		content = "<empty>"
	}

	if msg.Role == model.RoleTool && (msg.ToolName != "" || msg.ToolID != "") {
		return truncate(fmt.Sprintf("[%s %s] %s", msg.ToolName, msg.ToolID, content), 180)
	}

	if len(msg.ToolCalls) > 0 {
		return truncate(fmt.Sprintf("[tool_calls=%d] %s", len(msg.ToolCalls), content), 180)
	}

	return truncate(content, 180)
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func runTurn(
	ctx context.Context,
	r runner.Runner,
	userID, sessionID, label string,
	message model.Message,
	opts ...agent.RunOption,
) error {
	ctx = context.WithValue(ctx, scenarioKey{}, label)
	ch, err := r.Run(ctx, userID, sessionID, message, opts...)
	if err != nil {
		return err
	}

	var firstErr error
	for e := range ch {
		if firstErr == nil && e != nil && e.Error != nil {
			firstErr = fmt.Errorf("%s", e.Error.Message)
		}
	}
	return firstErr
}

func runScenario(
	ctx context.Context,
	r runner.Runner,
	userID, sessionID, name string,
	seed []model.Message,
	turn1Opts ...agent.RunOption,
) error {
	// Turn 1: seed history (only works when session is empty) and inject context.
	opts := append([]agent.RunOption{agent.WithMessages(seed)}, turn1Opts...)
	if err := runTurn(
		ctx,
		r,
		userID,
		sessionID,
		name+" / turn1 (with injection)",
		model.NewUserMessage("current: please handle this request"),
		opts...,
	); err != nil {
		return err
	}

	// Turn 2: do not inject. Late/injected context should NOT show up again.
	return runTurn(
		ctx,
		r,
		userID,
		sessionID,
		name+" / turn2 (no injection)",
		model.NewUserMessage("next: continue without extra rules"),
	)
}

func main() {
	ctx := context.Background()

	mdl := &debugModel{}
	llm := llmagent.New(
		"assistant",
		llmagent.WithModel(mdl),
		// Keep a stable prefix so the injection placement is easy to see.
		llmagent.WithGlobalInstruction("System: You are a helpful assistant."),
		llmagent.WithInstruction("Follow the user request and be concise."),
	)
	r := runner.NewRunner("rules-example", llm)

	seedHistory := []model.Message{
		model.NewUserMessage("history: hello"),
		model.NewAssistantMessage("history: hi"),
	}

	userID := "user-1"

	if err := runScenario(
		ctx,
		r,
		userID,
		"s-baseline",
		"baseline (no extra context)",
		seedHistory,
	); err != nil {
		log.Fatalf("baseline scenario failed: %v", err)
	}

	if err := runScenario(
		ctx,
		r,
		userID,
		"s-injected",
		"injected context (before session history)",
		seedHistory,
		agent.WithInjectedContextMessages([]model.Message{
			model.NewUserMessage("injected: background context A"),
			model.NewUserMessage("injected: background context B"),
		}),
	); err != nil {
		log.Fatalf("injected scenario failed: %v", err)
	}

	if err := runScenario(
		ctx,
		r,
		userID,
		"s-late",
		"late context (close to latest user)",
		seedHistory,
		agent.WithLateContextMessages([]model.Message{
			model.NewUserMessage("late: rules A (close to latest user)"),
			model.NewUserMessage("late: rules B (close to latest user)"),
		}),
	); err != nil {
		log.Fatalf("late scenario failed: %v", err)
	}
}

