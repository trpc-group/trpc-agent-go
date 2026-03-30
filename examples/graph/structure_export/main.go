//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates exporting a GraphAgent static structure snapshot.
package main

import (
	"context"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

func main() {
	ctx := context.Background()
	ag, err := buildAgent()
	if err != nil {
		log.Fatalf("Build graph agent failed: %v", err)
	}
	snapshot, err := structure.Export(ctx, ag)
	if err != nil {
		log.Fatalf("Export structure failed: %v", err)
	}
	printSnapshot(snapshot)
}
