//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/recorder"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	evalSetDir = flag.String("evalset-dir", "./output", "Directory where EvalSet files are stored")
	modelName  = flag.String("model", "gpt-5.2", "Model to use for the calculator agent")
	streaming  = flag.Bool("streaming", false, "Enable streaming responses from the agent")
	userID     = flag.String("user", "user-1", "User ID used by Runner")
	sessionID  = flag.String("session", "session-1", "Session ID used as EvalSetID/EvalCaseID by default")
)

const appName = "evalset-recorder-app"

func main() {
	flag.Parse()
	ctx := context.Background()
	// Create the evalset manager.
	manager := evalsetlocal.New(evalset.WithBaseDir(*evalSetDir))
	// Create the recorder plugin.
	rec, err := recorder.New(manager)
	if err != nil {
		log.Fatalf("create evalset recorder: %v", err)
	}
	// Create the runner with the recorder plugin.
	run := runner.NewRunner(appName, newCalculatorAgent(*modelName, *streaming), runner.WithPlugins(rec))
	// Run multiple turns in the same session.
	turns := []string{
		"i am 18 years old",
		"what is my age?",
		"calculate my age * 2",
	}
	for _, text := range turns {
		events, err := run.Run(ctx, *userID, *sessionID, model.NewUserMessage(text))
		if err != nil {
			log.Fatalf("runner run: %v", err)
		}
		for event := range events {
			data, err := json.Marshal(event)
			if err != nil {
				log.Fatalf("marshal event: %v", err)
			}
			fmt.Printf("event: %s\n", string(data))
		}
	}
	// Close the runner to release resources (and flush async writes if enabled).
	if err := run.Close(); err != nil {
		log.Fatalf("close runner: %v", err)
	}
	// Read and print the persisted EvalSet from disk.
	got, err := manager.Get(ctx, appName, *sessionID)
	if err != nil {
		log.Fatalf("load persisted eval set: %v", err)
	}
	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		log.Fatalf("marshal eval set: %v", err)
	}
	path := filepath.Join(*evalSetDir, appName, *sessionID+".evalset.json")
	fmt.Printf("evalset file: %s\n", path)
	fmt.Println(string(data))
}
