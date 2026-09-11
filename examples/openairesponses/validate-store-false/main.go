// Mini validator for OpenAI Responses + WithStore(false).
//
// Reproduces the dogfood failure mode where plaintext ReasoningContent was
// mapped to fabricated rs_replay_* item ids and OpenAI rejected the request.
//
// Modes:
//
//	(default) offline httptest capture — no API key
//	-live     real multi-turn tool loop against OpenAI
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openairesponses "trpc.group/trpc-go/trpc-agent-go/model/openai/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	live := flag.Bool("live", false, "Run live multi-turn OpenAI tool loop (needs OPENAI_API_KEY)")
	modelName := flag.String("model", envOr("OPENAI_MODEL", "gpt-5"), "Model name for -live")
	apiKey := flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "OpenAI API key for -live")
	baseURL := flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI base URL for -live")
	effort := flag.String("effort", "high", "Reasoning effort for -live (low|medium|high)")
	flag.Parse()

	var failed int
	failed += runCase("offline: omit plaintext reasoning under store=false", validateOfflineOmitReasoning)
	failed += runCase("offline: keep function_call + output without rs_replay_*", validateOfflineToolReplay)
	failed += runCase("offline: replay encrypted_content without fabricated id", validateOfflineEncryptedReplay)
	failed += runCase("offline: request include reasoning.encrypted_content", validateOfflineIncludeEncrypted)
	if *live {
		failed += runCase("live: multi-turn tool loop with store=false", func() error {
			return validateLiveMultiTurn(*modelName, *apiKey, *baseURL, *effort)
		})
	} else {
		fmt.Println("skip live (pass -live with OPENAI_API_KEY to exercise OpenAI)")
	}

	if failed > 0 {
		log.Fatalf("%d case(s) failed", failed)
	}
	fmt.Println("all cases passed")
}

func runCase(name string, fn func() error) int {
	fmt.Printf("==> %s\n", name)
	if err := fn(); err != nil {
		fmt.Printf("FAIL: %v\n", err)
		return 1
	}
	fmt.Println("PASS")
	return 0
}

// validateOfflineOmitReasoning captures the Responses request for history that
// contains only plaintext ReasoningContent (the Genie/session shape after a
// prior reasoning turn) and asserts no reasoning input item / rs_replay_* id.
func validateOfflineOmitReasoning() error {
	body, err := captureRequestBody([]model.Message{
		model.NewUserMessage("what is 2+2?"),
		{
			Role:             model.RoleAssistant,
			ReasoningContent: "I should use the calculator.",
			Content:          "let me calculate",
		},
		model.NewUserMessage("also multiply by 3"),
	})
	if err != nil {
		return err
	}
	return assertSafeStoreFalseBody(body, false)
}

// validateOfflineToolReplay is the incident shape: assistant with reasoning +
// tool_calls, then tool output, then a follow-up user turn.
func validateOfflineToolReplay() error {
	body, err := captureRequestBody([]model.Message{
		model.NewUserMessage("calc 123 * 456"),
		{
			Role:             model.RoleAssistant,
			ReasoningContent: "need a tool",
			ToolCalls: []model.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "calculator",
					Arguments: []byte(`{"operation":"multiply","a":123,"b":456}`),
				},
			}},
		},
		model.NewToolMessage("call_1", "calculator", `{"result":56088}`),
		model.NewUserMessage("what was that result again?"),
	})
	if err != nil {
		return err
	}
	if err := assertSafeStoreFalseBody(body, true); err != nil {
		return err
	}
	input, _ := body["input"].([]any)
	var sawCall, sawOutput bool
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		switch item["type"] {
		case "function_call":
			sawCall = true
			if item["call_id"] != "call_1" {
				return fmt.Errorf("function_call call_id=%v want call_1", item["call_id"])
			}
		case "function_call_output":
			sawOutput = true
			if item["call_id"] != "call_1" {
				return fmt.Errorf("function_call_output call_id=%v want call_1", item["call_id"])
			}
		}
	}
	if !sawCall || !sawOutput {
		return fmt.Errorf("expected function_call and function_call_output in input, got %#v", input)
	}
	return nil
}

func validateOfflineEncryptedReplay() error {
	body, err := captureRequestBody([]model.Message{
		model.NewUserMessage("calc"),
		{
			Role:               model.RoleAssistant,
			ReasoningContent:   "need a tool",
			ReasoningSignature: "enc_blob_from_prior_turn",
			ToolCalls: []model.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "calculator",
					Arguments: []byte(`{"operation":"add","a":1,"b":1}`),
				},
			}},
		},
		model.NewToolMessage("call_1", "calculator", `{"result":2}`),
		model.NewUserMessage("again?"),
	})
	if err != nil {
		return err
	}
	input, _ := body["input"].([]any)
	var sawReasoning bool
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		if item["type"] != "reasoning" {
			continue
		}
		sawReasoning = true
		if item["encrypted_content"] != "enc_blob_from_prior_turn" {
			return fmt.Errorf("encrypted_content=%v", item["encrypted_content"])
		}
		if id, ok := item["id"].(string); ok && id != "" {
			return fmt.Errorf("unexpected reasoning id %q under store=false", id)
		}
	}
	if !sawReasoning {
		return fmt.Errorf("expected reasoning item with encrypted_content, got %#v", input)
	}
	return nil
}

func validateOfflineIncludeEncrypted() error {
	body, err := captureRequestBody([]model.Message{model.NewUserMessage("hi")})
	if err != nil {
		return err
	}
	include, _ := body["include"].([]any)
	for _, v := range include {
		if s, ok := v.(string); ok && s == "reasoning.encrypted_content" {
			return nil
		}
	}
	return fmt.Errorf("include missing reasoning.encrypted_content: %#v", body["include"])
}

func captureRequestBody(messages []model.Message) (map[string]any, error) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"resp_validate",
			"object":"response",
			"status":"completed",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`)
	}))
	defer srv.Close()

	m := openairesponses.New(
		"gpt-5",
		openairesponses.WithAPIKey("test"),
		openairesponses.WithBaseURL(srv.URL),
		openairesponses.WithStore(false),
	)
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: messages,
		Tools:    map[string]tool.Tool{"calculator": stubCalc{}},
	})
	if err != nil {
		return nil, fmt.Errorf("GenerateContent: %w", err)
	}
	for resp := range ch {
		if resp.Error != nil {
			return nil, fmt.Errorf("model error: %s", resp.Error.Message)
		}
	}
	if gotBody == nil {
		return nil, fmt.Errorf("mock server received no request body")
	}
	return gotBody, nil
}

func assertSafeStoreFalseBody(body map[string]any, requireTools bool) error {
	store, _ := body["store"].(bool)
	if store {
		return fmt.Errorf("store=%v want false", body["store"])
	}
	input, ok := body["input"].([]any)
	if !ok {
		return fmt.Errorf("input missing or not an array: %#v", body["input"])
	}
	for i, raw := range input {
		item, _ := raw.(map[string]any)
		typ, _ := item["type"].(string)
		id, _ := item["id"].(string)
		if strings.HasPrefix(id, "rs_replay_") {
			return fmt.Errorf("input[%d] fabricated id %q", i, id)
		}
		if typ == "reasoning" {
			enc, _ := item["encrypted_content"].(string)
			if strings.TrimSpace(enc) == "" {
				return fmt.Errorf("input[%d] plaintext-only reasoning (need encrypted_content)", i)
			}
		}
	}
	_ = requireTools
	return nil
}

// validateLiveMultiTurn runs two turns against real OpenAI with store=false and
// high reasoning so the second turn includes prior tool+reasoning history.
func validateLiveMultiTurn(modelName, apiKey, baseURL, effort string) error {
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("OPENAI_API_KEY / -api-key required for -live")
	}
	opts := []openairesponses.Option{
		openairesponses.WithAPIKey(apiKey),
		openairesponses.WithStore(false),
	}
	if baseURL != "" {
		opts = append(opts, openairesponses.WithBaseURL(baseURL))
	}
	effortCopy := effort
	r := runner.NewRunner(
		"validate-store-false",
		llmagent.New(
			"calc-agent",
			llmagent.WithModel(openairesponses.New(modelName, opts...)),
			llmagent.WithDescription("Calculator assistant for store=false validation."),
			llmagent.WithInstruction("Use the calculator tool for arithmetic. Keep answers short."),
			llmagent.WithGenerationConfig(model.GenerationConfig{
				MaxTokens:       intPtr(2000),
				Stream:          false,
				ReasoningEffort: &effortCopy,
			}),
			llmagent.WithTools([]tool.Tool{calculatorTool()}),
		),
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	sessionID := fmt.Sprintf("validate-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	turns := []string{
		"Use the calculator tool to multiply 123 by 456. Reply with the number only after the tool returns.",
		"What was the calculator result from your previous tool call? Reply with the number only.",
	}
	for i, prompt := range turns {
		fmt.Printf("  live turn %d: %q\n", i+1, prompt)
		ch, err := r.Run(ctx, "validate-user", sessionID, model.NewUserMessage(prompt))
		if err != nil {
			return fmt.Errorf("turn %d Run: %w", i+1, err)
		}
		if err := drainLiveEvents(ch); err != nil {
			return fmt.Errorf("turn %d: %w", i+1, err)
		}
	}
	return nil
}

func drainLiveEvents(ch <-chan *event.Event) error {
	var lastErr string
	for evt := range ch {
		if evt.Response == nil {
			continue
		}
		if evt.Response.Error != nil {
			msg := evt.Response.Error.Message
			lastErr = msg
			if strings.Contains(msg, "rs_replay_") || strings.Contains(msg, "store is set to false") {
				return fmt.Errorf("OpenAI rejected fabricated reasoning replay: %s", msg)
			}
		}
		if len(evt.Response.Choices) > 0 && len(evt.Response.Choices[0].Message.ToolCalls) > 0 {
			for _, tc := range evt.Response.Choices[0].Message.ToolCalls {
				fmt.Printf("    tool %s %s\n", tc.Function.Name, string(tc.Function.Arguments))
			}
		}
		if evt.IsFinalResponse() && len(evt.Response.Choices) > 0 {
			content := evt.Response.Choices[0].Message.Content
			if content != "" {
				fmt.Printf("    assistant: %s\n", truncate(content, 120))
			}
		}
	}
	if lastErr != "" {
		return fmt.Errorf("model error: %s", lastErr)
	}
	return nil
}

func calculatorTool() tool.Tool {
	type args struct {
		Operation string  `json:"operation" jsonschema:"description=The operation to perform,enum=add,enum=subtract,enum=multiply,enum=divide"`
		A         float64 `json:"a"`
		B         float64 `json:"b"`
	}
	type result struct {
		Result float64 `json:"result"`
	}
	return function.NewFunctionTool(func(_ context.Context, in args) (result, error) {
		var out float64
		switch strings.ToLower(in.Operation) {
		case "add", "+":
			out = in.A + in.B
		case "subtract", "-":
			out = in.A - in.B
		case "multiply", "*":
			out = in.A * in.B
		case "divide", "/":
			if in.B == 0 {
				return result{}, fmt.Errorf("division by zero")
			}
			out = in.A / in.B
		default:
			return result{}, fmt.Errorf("unsupported operation %q", in.Operation)
		}
		return result{Result: out}, nil
	}, function.WithName("calculator"), function.WithDescription("Perform basic mathematical calculations"))
}

type stubCalc struct{}

func (stubCalc) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "calculator", Description: "calc"}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func intPtr(i int) *int { return &i }

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
