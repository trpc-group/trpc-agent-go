//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to use the Batch APIs with the OpenAI-like
// model in trpc-agent-go.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	openaisdk "github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Constants to avoid magic strings and provide sane defaults.
const (
	defaultModelName = "gpt-4o-mini"
	defaultAction    = "list" // Supported: create|get|cancel|list.
	defaultLimit     = int64(5)
	defaultWindow    = "24h" // Batch completion window.
)

func main() {
	// CLI flags for different actions.
	action := flag.String("action", defaultAction, "Action: create|"+
		"get|cancel|list")
	modelName := flag.String("model", defaultModelName, "Model name to use")
	maxRetries := flag.Int("retries", 3, "Max HTTP retries for SDK requests")
	timeout := flag.Duration("timeout", 30*time.Second, "Request timeout")

	// Create options: user-provided requests.
	requestsInline := flag.String("requests", "", "Inline requests spec. "+
		"Format: 'role: msg || role: msg /// role: msg || role: msg'.")
	requestsFile := flag.String("file", "", "Path to requests spec file. "+
		"Same format as -requests, '///' between requests, '||' between messages.")

	// Flags for get/cancel.
	batchID := flag.String("id", "", "Batch ID for get/cancel")

	// Flags for list.
	after := flag.String("after", "", "Pagination cursor for listing batches")
	limit := flag.Int64("limit", defaultLimit, "Max number of batches to list "+
		"(1-100)")

	flag.Parse()

	fmt.Printf("🚀 Using configuration:\n")
	fmt.Printf("   📝 Model Name: %s\n", *modelName)
	fmt.Printf("   🎛️  Action: %s\n", *action)
	fmt.Printf("   🔑 OpenAI SDK reads OPENAI_API_KEY and OPENAI_BASE_URL from env\n")
	fmt.Println()

	// Initialize model with retry and timeout options.
	llm := openai.New(*modelName,
		openai.WithOpenAIOptions(
			openaiopt.WithMaxRetries(*maxRetries),
			openaiopt.WithRequestTimeout(*timeout),
		),
	)

	ctx := context.Background()

	var err error
	switch *action {
	case "create":
		err = runCreate(ctx, llm, *requestsInline, *requestsFile)
	case "get":
		err = runGet(ctx, llm, *batchID)
	case "cancel":
		err = runCancel(ctx, llm, *batchID)
	case "list":
		err = runList(ctx, llm, *after, *limit)
	default:
		err = fmt.Errorf("unknown action: %s", *action)
	}

	if err != nil {
		log.Printf("❌ %v", err)
	} else {
		fmt.Println("🎉 Done.")
	}
}

// runCreate builds requests from user spec and creates a batch.
func runCreate(
	ctx context.Context,
	llm *openai.Model,
	inlineSpec string,
	filePath string,
) error {
	if inlineSpec == "" && filePath == "" {
		return errors.New("provide -requests or -file for create")
	}

	spec := inlineSpec
	if spec == "" {
		b, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		spec = strings.TrimSpace(string(b))
	}

	requests, err := parseRequestsSpec(spec)
	if err != nil {
		return fmt.Errorf("invalid requests spec: %w", err)
	}
	if len(requests) == 0 {
		return errors.New("no requests parsed")
	}

	batch, err := llm.CreateBatch(
		ctx,
		requests,
		openai.WithBatchCreateCompletionWindow(defaultWindow),
	)
	if err != nil {
		return fmt.Errorf("failed to create batch: %w", err)
	}

	printBatch("🆕 Batch created.", batch)
	return nil
}

// parseRequestsSpec parses a simple textual spec into batch requests.
// Requests are separated by '///'. Messages within a request are separated by
// '||'. Each message line uses 'role: content'. Roles: system|user|assistant.
func parseRequestsSpec(spec string) ([]*openai.BatchRequestInput, error) {
	var out []*openai.BatchRequestInput
	chunks := splitBy(spec, "///")
	for i, chunk := range chunks {
		if chunk == "" {
			continue
		}
		lines := splitBy(chunk, "||")
		var req openai.BatchRequest
		for _, ln := range lines {
			role, content, ok := splitRoleContent(ln)
			if !ok {
				return nil, fmt.Errorf("invalid message format: %q", ln)
			}
			switch role {
			case "system":
				req.Messages = append(req.Messages, model.NewSystemMessage(content))
			case "user":
				req.Messages = append(req.Messages, model.NewUserMessage(content))
			case "assistant":
				req.Messages = append(req.Messages, model.NewAssistantMessage(content))
			default:
				req.Messages = append(req.Messages, model.Message{Role: model.Role(role), Content: content})
			}
		}
		if len(req.Messages) == 0 {
			continue
		}
		customID := fmt.Sprintf("%03d", i+1)
		out = append(out, &openai.BatchRequestInput{
			CustomID: customID,
			Method:   "POST",
			URL:      string(openaisdk.BatchNewParamsEndpointV1ChatCompletions),
			Body:     req,
		})
	}
	return out, nil
}

// splitBy splits by sep and trims spaces for each piece.
func splitBy(s, sep string) []string {
	parts := strings.Split(s, sep)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// splitRoleContent splits 'role: content' into parts.
func splitRoleContent(s string) (role, content string, ok bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

// runGet retrieves a batch by ID.
func runGet(ctx context.Context, llm *openai.Model, id string) error {
	if id == "" {
		return errors.New("id is required for get")
	}
	batch, err := llm.RetrieveBatch(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to retrieve batch: %w", err)
	}
	printBatch("🔎 Batch details.", batch)

	// If output is available, download and parse it.
	if batch.OutputFileID != "" {
		fmt.Printf("\n📥 Downloading and parsing output file: %s\n",
			batch.OutputFileID)
		text, err := llm.DownloadFileContent(ctx, batch.OutputFileID)
		if err != nil {
			return fmt.Errorf("failed to download output file: %w", err)
		}
		entries, err := llm.ParseBatchOutput(text)
		if err != nil {
			return fmt.Errorf("failed to parse output file: %w", err)
		}
		for _, e := range entries {
			fmt.Printf("[%s] status=%d\n", e.CustomID, e.Response.StatusCode)
			if content, ok := firstChatContent(e.Response.Body); ok {
				fmt.Printf("  content: %s\n", content)
			}
			if len(e.Error) > 0 {
				fmt.Printf("  error: %s\n", string(e.Error))
			}
		}
	}
	return nil
}

// runCancel cancels a batch by ID.
func runCancel(ctx context.Context, llm *openai.Model, id string) error {
	if id == "" {
		return errors.New("id is required for cancel")
	}
	batch, err := llm.CancelBatch(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to cancel batch: %w", err)
	}
	printBatch("🛑 Batch cancel requested.", batch)
	return nil
}

// runList lists batches with pagination.
func runList(ctx context.Context, llm *openai.Model, after string, limit int64) error {
	if limit <= 0 {
		limit = defaultLimit
	}
	page, err := llm.ListBatches(ctx, after, limit)
	if err != nil {
		return fmt.Errorf("failed to list batches: %w", err)
	}
	fmt.Printf("📃 Listing up to %d batches (after=%s).\n", limit, after)
	for i, item := range page.Data {
		fmt.Printf("%2d. id=%s status=%s created=%s requests(total=%d,ok=%d,fail=%d)\n",
			i+1,
			item.ID,
			string(item.Status),
			ts(item.CreatedAt),
			item.RequestCounts.Total,
			item.RequestCounts.Completed,
			item.RequestCounts.Failed,
		)
	}
	if page.HasMore && len(page.Data) > 0 {
		last := page.Data[len(page.Data)-1]
		fmt.Printf("➡️  More available. Use --after=%s for next page.\n", last.ID)
	}
	return nil
}

// printBatch prints key information of a batch.
func printBatch(prefix string, b *openaisdk.Batch) {
	fmt.Printf("%s\n", prefix)
	fmt.Printf("   🆔 ID: %s\n", b.ID)
	fmt.Printf("   🔗 Endpoint: %s\n", b.Endpoint)
	fmt.Printf("   🕐 Created: %s\n", ts(b.CreatedAt))
	fmt.Printf("   🧭 Status: %s\n", b.Status)
	fmt.Printf("   📥 Input File: %s\n", b.InputFileID)
	if b.OutputFileID != "" {
		fmt.Printf("   📤 Output File: %s\n", b.OutputFileID)
	}
	if b.ErrorFileID != "" {
		fmt.Printf("   ⚠️  Error File: %s\n", b.ErrorFileID)
	}
	fmt.Printf("   📊 Requests: total=%d ok=%d fail=%d\n",
		b.RequestCounts.Total,
		b.RequestCounts.Completed,
		b.RequestCounts.Failed,
	)
}

// ts renders a unix seconds timestamp.
func ts(sec int64) string {
	if sec <= 0 {
		return "-"
	}
	return time.Unix(sec, 0).Format(time.RFC3339)
}

// firstChatContent extracts the first message content from a chat completions
// response body.
func firstChatContent(body json.RawMessage) (string, bool) {
	var tmp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &tmp); err != nil {
		return "", false
	}
	if len(tmp.Choices) == 0 {
		return "", false
	}
	return tmp.Choices[0].Message.Content, tmp.Choices[0].Message.Content != ""
}

// toRequest converts a simple map to BatchInputBody.
func toRequest(m map[string]any) openai.BatchRequest {
	var req openai.BatchRequest
	// Messages.
	if msgs, ok := m["messages"].([]map[string]string); ok {
		for _, mm := range msgs {
			role := mm["role"]
			content := mm["content"]
			switch role {
			case "system":
				req.Messages = append(req.Messages, model.NewSystemMessage(content))
			case "user":
				req.Messages = append(req.Messages, model.NewUserMessage(content))
			case "assistant":
				req.Messages = append(req.Messages, model.NewAssistantMessage(content))
			default:
				req.Messages = append(req.Messages, model.Message{Role: model.Role(role), Content: content})
			}
		}
	}
	// Temperature.
	if v, ok := m["temperature"].(float64); ok {
		t := v
		req.Temperature = &t
	}
	// TopP.
	if v, ok := m["top_p"].(float64); ok {
		t := v
		req.TopP = &t
	}
	// MaxTokens.
	if v, ok := m["max_tokens"].(float64); ok {
		iv := int(v)
		req.MaxTokens = &iv
	}
	// Stop.
	if v, ok := m["stop"].([]string); ok {
		req.Stop = v
	}
	return req
}
