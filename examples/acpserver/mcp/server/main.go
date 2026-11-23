//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main exposes an ACP server used by the stdio MCP example.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"go.uber.org/zap"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	acpserver "trpc.group/trpc-go/trpc-agent-go/server/acp"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if err := configureLogging(); err != nil {
		return err
	}
	modelName := flag.String("model", "gpt-4o-mini", "OpenAI model name")
	flag.Parse()

	agent := llmagent.New(
		"acp-mcp-assistant",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription("An ACP assistant using session-scoped MCP tools"),
		llmagent.WithInstruction(
			"You are an ACP stdio MCP example. When the user names an MCP tool, "+
				"call that tool and answer using its actual result. MCP tool names "+
				"include the server prefix shown in their declarations.",
		),
	)
	r := runner.NewRunner("acp-mcp-example", agent)
	defer r.Close()

	server, err := acpserver.New(
		r,
		acpserver.WithImplementation("trpc-agent-go-acp-mcp-example", "1.0.0"),
	)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil &&
		!errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func configureLogging() error {
	config := zap.NewProductionConfig()
	logger, err := config.Build()
	if err != nil {
		return fmt.Errorf("configure logging: %w", err)
	}
	sugar := logger.Sugar()
	log.Default = sugar
	log.ContextDefault = sugar
	return nil
}
