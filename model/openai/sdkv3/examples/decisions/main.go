//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/decision"
	"trpc.group/trpc-go/trpc-agent-go/decision/systemone"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/decisions"
)

func main() {
	backend := flag.String("backend", "openai", "decision backend: openai or systemone")
	model := flag.String("model", "", "model name (defaults to gpt-6-luna for OpenAI)")
	flag.Parse()

	d, err := newDecider(*backend, *model)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := d.Decide(ctx, decision.Request{
		Input: decision.Input{Text: "My package is late. Can you check where it is?"},
		Questions: []decision.Question{{
			ID: "topic", Kind: decision.Choice, Instructions: "Classify the ticket by its main topic.",
			Options: []decision.Option{
				{ID: "shipping", Description: "Package status or delivery"},
				{ID: "billing", Description: "Payments or refunds"},
				{ID: "other", Description: "Neither shipping nor billing"},
			},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	answer := result.Answers["topic"]
	fmt.Printf("model=%s request_id=%s status=%s", result.Model, result.RequestID, answer.Status)
	if answer.Status == decision.Answered {
		fmt.Printf(" choice=%q", answer.Choice)
	}
	fmt.Println()
}

func newDecider(backend, model string) (decision.Decider, error) {
	switch backend {
	case "openai":
		if model == "" {
			model = "gpt-6-luna"
		}
		return decisions.New(model)
	case "systemone":
		return systemone.New(systemone.Config{
			BaseURL: os.Getenv("SYSTEMONE_BASE_URL"),
			APIKey:  os.Getenv("SYSTEMONE_API_KEY"),
			Model:   model,
		})
	default:
		return nil, fmt.Errorf("unknown decision backend %q", backend)
	}
}
