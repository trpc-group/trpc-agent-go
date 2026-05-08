//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates DeferredToolSet inside a GraphAgent loop.
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
	prompt := flag.String("prompt", demo.GraphPrompt, "Prompt to send to the graph")
	flag.Parse()

	fmt.Println("DeferredToolSet graph example")
	fmt.Println("Hint: from trpc-agent-go/examples, run `source ../dpskv4.sh` before real model calls.")
	fmt.Println()

	result, err := demo.RunGraph(context.Background(), demo.RunConfig{
		ModelName: *modelName,
		Prompt:    *prompt,
		Output:    os.Stdout,
	})
	if err != nil {
		log.Fatalf("DeferredToolSet graph example failed: %v", err)
	}

	fmt.Printf("\nFinal answer: %s\n", result.FinalText)
}
