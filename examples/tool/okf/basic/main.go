//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command basic demonstrates consuming a local OKF bundle and exposing it to
// an LLM agent as tools.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/okf"
	"trpc.group/trpc-go/trpc-agent-go/tool/okf/localokf"
)

func main() {
	bundleDir := flag.String("bundle", "bundle", "path to an OKF bundle")
	flag.Parse()

	ctx := context.Background()
	store, err := localokf.New(*bundleDir)
	if err != nil {
		log.Fatal(err)
	}

	listing, err := store.List(ctx, "")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("bundle version: %s\n", listing.OKFVersion)
	fmt.Printf("root subdirectories: %v\n", listing.Subdirs)

	concept, err := store.Read(ctx, "research/x402")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("concept %q: type=%s title=%q links=%v\n",
		concept.ID, concept.Frontmatter.Type, concept.Frontmatter.Title, concept.Links)

	toolSet, err := okf.NewToolSet(store)
	if err != nil {
		log.Fatal(err)
	}
	agent := llmagent.New(
		"okf-agent",
		llmagent.WithToolSets([]tool.ToolSet{toolSet}),
	)
	for _, t := range agent.UserTools() {
		fmt.Printf("agent tool: %s\n", t.Declaration().Name)
	}
}
