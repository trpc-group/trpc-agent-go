//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a pure DeferredToolSet runtime tool-search flow.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/examples/tool/deferredtoolset/demo"
)

func main() {
	modelName := flag.String("model", demo.DefaultModelName(), "Model name to use")
	prompt := flag.String("prompt", demo.BasicPrompt, "Prompt to send to the agent")
	flag.Parse()

	fmt.Println("DeferredToolSet basic example")
	fmt.Println("Hint: from trpc-agent-go/examples, run `source ../dpskv4.sh` before real model calls.")
	fmt.Println()

	result, err := demo.RunBasic(context.Background(), demo.RunConfig{
		ModelName: *modelName,
		Prompt:    *prompt,
		Output:    os.Stdout,
	})
	if err != nil {
		log.Fatalf("basic DeferredToolSet example failed: %v", err)
	}

	fmt.Printf("\nFinal answer: %s\n", result.FinalText)
}
