//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates sync-summary cleanup for DeferredToolSet session
// mirrors while preserving current-invocation loaded tools.
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

	fmt.Println("DeferredToolSet sync summary cleanup example")
	fmt.Println("Hint: from trpc-agent-go/examples, run `source ../dpskv4.sh` before real model calls.")
	fmt.Println()

	result, err := demo.RunSummarySyncCleanup(context.Background(), demo.RunConfig{
		ModelName: *modelName,
		Output:    os.Stdout,
	})
	if err != nil {
		log.Fatalf("sync summary cleanup DeferredToolSet example failed: %v", err)
	}

	if len(result.TurnFinalTexts) > 0 && len(result.TurnToolCalls) > 0 {
		fmt.Printf(
			"\nFinal answer: %s\nTool calls: %v\n",
			result.TurnFinalTexts[0],
			result.TurnToolCalls[0],
		)
	}
	fmt.Println("\nSmoke check: sync summary cleanup behaved as expected.")
}
