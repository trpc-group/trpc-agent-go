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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	openaisdk "github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Constants to avoid magic strings and provide sane defaults.
const (
	defaultModelName = "gpt-4o-mini"
	defaultAction    = "create" // Supported: create|get|cancel|list.
	defaultLimit     = int64(5)
	defaultWindow    = "24h" // Batch completion window.
	methodPOST       = "POST"
)

// batchJSONL represents a single JSONL line for batch input file.
type batchJSONL struct {
	CustomID string      `json:"custom_id"`
	Method   string      `json:"method"`
	URL      string      `json:"url"`
	Body     interface{} `json:"body"`
}

func main() {
	// CLI flags for different actions.
	action := flag.String("action", defaultAction, "Action: create|get|cancel|list")
	modelName := flag.String("model", defaultModelName, "Model name to use")
	maxRetries := flag.Int("retries", 3, "Max HTTP retries for SDK requests")
	timeout := flag.Duration("timeout", 30*time.Second, "Request timeout")

	// Flags for create.
	endpointStr := flag.String("endpoint", string(openaisdk.BatchNewParamsEndpointV1ChatCompletions), "Endpoint for batch: /v1/chat/completions|/v1/responses|/v1/embeddings|/v1/completions")
	prompt := flag.String("prompt", "Say hello from batch.", "Prompt used to generate JSONL body")
	count := flag.Int("count", 3, "How many requests to include in JSONL input")
	inputPath := flag.String("input", "", "Optional path to an existing JSONL file; if empty a temp file is generated")

	// Flags for get/cancel.
	batchID := flag.String("id", "", "Batch ID for get/cancel")

	// Flags for list.
	after := flag.String("after", "", "Pagination cursor for listing batches")
	limit := flag.Int64("limit", defaultLimit, "Max number of batches to list (1-100)")

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
		err = runCreate(ctx, llm, *modelName, *endpointStr, *prompt, *count, *inputPath)
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

// runCreate uploads a JSONL file (or uses given path) and creates a batch.
func runCreate(
	ctx context.Context,
	llm *openai.Model,
	modelName string,
	endpointStr string,
	prompt string,
	count int,
	inputPath string,
) error {
	if count <= 0 {
		return errors.New("count must be > 0")
	}

	endpoint, err := toBatchEndpoint(endpointStr)
	if err != nil {
		return err
	}

	// Prepare JSONL file if not provided.
	var filePath string
	if inputPath != "" {
		filePath = inputPath
		fmt.Printf("📄 Using existing JSONL file: %s\n", filePath)
	} else {
		fmt.Println("🛠️  Generating temporary JSONL input file...")
		tmp, err := os.CreateTemp("", "batch-input-*.jsonl")
		if err != nil {
			return fmt.Errorf("failed to create temp file: %w", err)
		}
		defer tmp.Close()
		writer := bufio.NewWriter(tmp)

		// Construct JSONL lines based on endpoint.
		for i := 0; i < count; i++ {
			line, err := buildJSONLLine(i, endpoint, modelName, prompt)
			if err != nil {
				return err
			}
			bytes, err := json.Marshal(line)
			if err != nil {
				return fmt.Errorf("failed to marshal jsonl line: %w", err)
			}
			if _, err := writer.Write(bytes); err != nil {
				return fmt.Errorf("failed to write jsonl line: %w", err)
			}
			if err := writer.WriteByte('\n'); err != nil {
				return fmt.Errorf("failed to write newline: %w", err)
			}
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("failed to flush writer: %w", err)
		}
		filePath = tmp.Name()
		fmt.Printf("📦 JSONL file generated: %s\n", filePath)
	}

	// Upload file with purpose set to "batch".
	const filePurposeBatch openaisdk.FilePurpose = openaisdk.FilePurpose("batch")
	fileID, err := llm.UploadFile(ctx, filePath, openai.WithPurpose(filePurposeBatch))
	if err != nil {
		return fmt.Errorf("failed to upload jsonl file: %w", err)
	}
	fmt.Printf("☁️  Uploaded file. File ID: %s\n", fileID)

	// Create the batch.
	params := openaisdk.BatchNewParams{
		CompletionWindow: openaisdk.BatchNewParamsCompletionWindow(defaultWindow),
		Endpoint:         endpoint,
		InputFileID:      fileID,
	}
	batch, err := llm.CreateBatch(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to create batch: %w", err)
	}

	printBatch("🆕 Batch created.", batch)
	return nil
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

// buildJSONLLine constructs one request line for the batch input file.
func buildJSONLLine(
	index int,
	endpoint openaisdk.BatchNewParamsEndpoint,
	modelName, prompt string,
) (batchJSONL, error) {
	customID := fmt.Sprintf("req-%d", index+1)
	switch endpoint {
	case openaisdk.BatchNewParamsEndpointV1ChatCompletions:
		body := map[string]interface{}{
			"model": modelName,
			"messages": []map[string]string{
				{"role": "user", "content": fmt.Sprintf("%s #%d", prompt, index+1)},
			},
		}
		return batchJSONL{
			CustomID: customID,
			Method:   methodPOST,
			URL:      string(openaisdk.BatchNewParamsEndpointV1ChatCompletions),
			Body:     body,
		}, nil
	case openaisdk.BatchNewParamsEndpointV1Completions:
		body := map[string]interface{}{
			"model":  modelName,
			"prompt": fmt.Sprintf("%s #%d", prompt, index+1),
		}
		return batchJSONL{CustomID: customID, Method: methodPOST, URL: string(openaisdk.BatchNewParamsEndpointV1Completions), Body: body}, nil
	case openaisdk.BatchNewParamsEndpointV1Embeddings:
		body := map[string]interface{}{
			"model": modelName,
			"input": []string{fmt.Sprintf("%s #%d", prompt, index+1)},
		}
		return batchJSONL{CustomID: customID, Method: methodPOST, URL: string(openaisdk.BatchNewParamsEndpointV1Embeddings), Body: body}, nil
	case openaisdk.BatchNewParamsEndpointV1Responses:
		body := map[string]interface{}{
			"model": modelName,
			"input": fmt.Sprintf("%s #%d", prompt, index+1),
		}
		return batchJSONL{CustomID: customID, Method: methodPOST, URL: string(openaisdk.BatchNewParamsEndpointV1Responses), Body: body}, nil
	default:
		return batchJSONL{}, fmt.Errorf("unsupported endpoint: %s", endpoint)
	}
}

// toBatchEndpoint validates and converts a string into the typed endpoint value.
func toBatchEndpoint(s string) (openaisdk.BatchNewParamsEndpoint, error) {
	switch openaisdk.BatchNewParamsEndpoint(s) {
	case openaisdk.BatchNewParamsEndpointV1ChatCompletions,
		openaisdk.BatchNewParamsEndpointV1Completions,
		openaisdk.BatchNewParamsEndpointV1Embeddings,
		openaisdk.BatchNewParamsEndpointV1Responses:
		return openaisdk.BatchNewParamsEndpoint(s), nil
	default:
		return "", fmt.Errorf("invalid endpoint: %s", s)
	}
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
