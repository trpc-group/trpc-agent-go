//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main is the main package for the AG-UI server.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Model to use")
	isStream  = flag.Bool("stream", true, "Whether to stream the response")
	address   = flag.String("address", "127.0.0.1:8080", "Listen address")
	path      = flag.String("path", "/agui", "HTTP path")
)

const reportInstruction = `You are reportAgent, responsible for drafting structured business reports.
Workflow:
1. Before any tool calls, send a short assistant sentence explaining that you are preparing a document.
2. Then call open_report_document and pick the title from the latest user request.
3. After the open tool call succeeds, write the full report as Assistant text. Keep it concise but actionable.
4. Call close_report_document once the report is streamed.
5. After closing, send one final assistant line summarizing the takeaway and noting the doc is done.
Only use English in tool inputs; the visible report can mirror the user's language.`

func main() {
	flag.Parse()
	modelInstance := openai.New(*modelName)
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(800),
		Temperature: floatPtr(0.4),
		Stream:      *isStream,
	}

	openTool := function.NewFunctionTool(
		openReportDocument,
		function.WithName("open_report_document"),
		function.WithDescription("Open a document box in the AG-UI frontend before emitting the textual report."),
	)
	closeTool := function.NewFunctionTool(
		closeReportDocument,
		function.WithName("close_report_document"),
		function.WithDescription("Close the active AG-UI document box after the report is delivered."),
	)

	agent := llmagent.New(
		"report-agent",
		llmagent.WithTools([]tool.Tool{openTool, closeTool}),
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction(reportInstruction),
	)

	runner := runner.NewRunner(agent.Info().Name, agent)
	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0).
	defer runner.Close()

	server, err := agui.New(runner, agui.WithPath(*path))
	if err != nil {
		log.Fatalf("failed to create AG-UI server: %v", err)
	}

	log.Infof("AG-UI: serving agent %q on http://%s%s", agent.Info().Name, *address, *path)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

func intPtr(i int) *int { return &i }

func floatPtr(f float64) *float64 { return &f }

type openReportArgs struct {
	Title string `json:"title" description:"Document box title"`
}

type openReportResult struct {
	Title      string `json:"title"`
	DocumentID string `json:"documentId"`
	CreatedAt  string `json:"createdAt"`
}

type closeReportArgs struct {
	Reason string `json:"reason" description:"Why the document is being closed"`
}

type closeReportResult struct {
	Closed   bool   `json:"closed"`
	Message  string `json:"message"`
	ClosedAt string `json:"closedAt"`
}

func openReportDocument(ctx context.Context, args openReportArgs) (openReportResult, error) {
	_ = ctx
	title := strings.TrimSpace(args.Title)
	if title == "" {
		title = "Auto generated report"
	}
	return openReportResult{
		Title:      title,
		DocumentID: uuid.NewString(),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func closeReportDocument(ctx context.Context, args closeReportArgs) (closeReportResult, error) {
	_ = ctx
	reason := strings.TrimSpace(args.Reason)
	if reason == "" {
		reason = "report_completed"
	}
	msg := fmt.Sprintf("document box closed: %s", reason)
	return closeReportResult{
		Closed:   true,
		Message:  msg,
		ClosedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}
