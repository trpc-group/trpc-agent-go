//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main exposes an ACP server that enables client file tools.
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
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	modelName := flag.String("model", "gpt-4o-mini", "OpenAI model name")
	flag.Parse()

	agent := llmagent.New(
		"acp-client-files-assistant",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription("An ACP assistant using client file tools"),
		llmagent.WithInstruction(
			"You are an ACP client filesystem example. When asked to read or write "+
				"a file, use acp_read_text_file and acp_write_text_file. Paths are "+
				"relative to the session workspace described by the tools. Never "+
				"claim a file operation succeeded without using the corresponding tool.",
		),
	)
	r := runner.NewRunner("acp-client-files-example", agent)
	defer r.Close()

	server, err := acpserver.New(
		r,
		acpserver.WithImplementation("trpc-agent-go-acp-client-files-example", "1.0.0"),
		acpserver.WithClientFileReadEnabled(true),
		acpserver.WithClientFileWriteEnabled(true),
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
