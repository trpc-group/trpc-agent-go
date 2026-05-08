//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates async-summary cleanup for DeferredToolSet session
// mirrors across two user turns.
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
	flag.Parse()

	fmt.Println("DeferredToolSet async summary cleanup example")
	fmt.Println("Hint: from trpc-agent-go/examples, run `source ../dpskv4.sh` before real model calls.")
	fmt.Println()

	result, err := demo.RunSummaryAsyncCleanup(context.Background(), demo.RunConfig{
		ModelName: *modelName,
		Output:    os.Stdout,
	})
	if err != nil {
		log.Fatalf("async summary cleanup DeferredToolSet example failed: %v", err)
	}

	if len(result.TurnFinalTexts) >= 2 && len(result.TurnToolCalls) >= 2 {
		fmt.Printf(
			"\nTurn 1 final answer: %s\nTurn 1 tool calls: %v\n"+
				"Turn 2 final answer: %s\nTurn 2 tool calls: %v\n",
			result.TurnFinalTexts[0],
			result.TurnToolCalls[0],
			result.TurnFinalTexts[1],
			result.TurnToolCalls[1],
		)
	}
	fmt.Println("\nSmoke check: async summary cleanup behaved as expected.")
}
