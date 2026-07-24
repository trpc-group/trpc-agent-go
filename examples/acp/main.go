//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main exposes a basic LLM agent over ACP stdio.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	acpserver "trpc.group/trpc-go/trpc-agent-go/server/acp"
)

func main() {
	modelName := flag.String("model", "gpt-4o-mini", "OpenAI model name")
	flag.Parse()

	agent := llmagent.New(
		"acp-assistant",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription("A helpful assistant exposed through ACP"),
		llmagent.WithInstruction("Answer the user's request clearly and concisely."),
	)
	r := runner.NewRunner("acp-example", agent)
	defer r.Close()

	server, err := acpserver.New(
		r,
		acpserver.WithImplementation("trpc-agent-go-example", "1.0.0"),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil &&
		!errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
