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
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	openaisdk "github.com/openai/openai-go"
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

	// Initialize model.
	llm := openai.New(*modelName)

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
			// Extract first content from ChatCompletion body.
			if len(e.Response.Body.Choices) > 0 {
				fmt.Printf("  content: %s\n", e.Response.Body.Choices[0].Message.Content)
			}
			// Print error details if present.
			if e.Error != nil {
				fmt.Printf("  error: type=%s message=%s\n", e.Error.Type, e.Error.Message)
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
	if after != "" {
		fmt.Printf("📃 Listing up to %d batches (after=%s).\n", limit, after)
	} else {
		fmt.Printf("📃 Listing up to %d batches.\n", limit)
	}
	for i, item := range page.Data {
		printBatchListItem(i+1, item)
	}
	if page.HasMore && len(page.Data) > 0 {
		last := page.Data[len(page.Data)-1]
		fmt.Printf("➡️  More available. Use --after=%s for next page.\n", last.ID)
	}
	return nil
}

// printBatchListItem prints detailed information for a single batch item in the list.
func printBatchListItem(index int, item openaisdk.Batch) {
	fmt.Printf("%2d. id=%s status=%s created=%s requests(total=%d,ok=%d,fail=%d)\n",
		index,
		item.ID,
		string(item.Status),
		ts(item.CreatedAt),
		item.RequestCounts.Total,
		item.RequestCounts.Completed,
		item.RequestCounts.Failed,
	)

	// Show additional details for each batch.
	if item.OutputFileID != "" {
		fmt.Printf("     📤 Output: %s\n", item.OutputFileID)
	}
	if item.ErrorFileID != "" {
		fmt.Printf("     ⚠️  Error File: %s\n", item.ErrorFileID)
	}
	if len(item.Errors.Data) > 0 {
		fmt.Printf("     ❌ Errors:\n")
		for j, err := range item.Errors.Data {
			fmt.Printf("        %d. Code: %s, Message: %s\n",
				j+1,
				err.Code,
				err.Message)
		}
	}
	if len(item.Metadata) > 0 {
		fmt.Printf("     🏷️  Metadata: %v\n", item.Metadata)
	}
	if item.CompletionWindow != "" {
		fmt.Printf("     ⏰ Window: %s\n", item.CompletionWindow)
	}
	if item.ExpiresAt != 0 {
		fmt.Printf("     ⏳ Expires: %s\n", ts(item.ExpiresAt))
	}
	if item.CompletedAt != 0 {
		fmt.Printf("     ✅ Completed: %s\n", ts(item.CompletedAt))
	}
	if item.FailedAt != 0 {
		fmt.Printf("     💥 Failed: %s\n", ts(item.FailedAt))
	}
	if item.CancelledAt != 0 {
		fmt.Printf("     🚫 Cancelled: %s\n", ts(item.CancelledAt))
	}
	if item.InProgressAt != 0 {
		fmt.Printf("     🔄 In Progress: %s\n", ts(item.InProgressAt))
	}
	if item.FinalizingAt != 0 {
		fmt.Printf("     🎯 Finalizing: %s\n", ts(item.FinalizingAt))
	}
	fmt.Println() // Add empty line between batches for readability.
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

	// Print errors if available.
	if len(b.Errors.Data) > 0 {
		fmt.Printf("   ❌ Errors:\n")
		for i, err := range b.Errors.Data {
			fmt.Printf("      %d. Code: %s, Message: %s\n",
				i+1,
				err.Code,
				err.Message)
		}
	}
}

// ts renders a unix seconds timestamp.
func ts(sec int64) string {
	if sec <= 0 {
		return "-"
	}
	return time.Unix(sec, 0).Format(time.RFC3339)
}
