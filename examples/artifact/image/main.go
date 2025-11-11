//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"flag"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

func main() {
	// Parse command line arguments
	modelName := flag.String("model", "deepseek-chat", "Model name to use")
	flag.Parse()
	imageGenerateAgent := newImageGenerateAgent("image_app", "image_generate_agent", *modelName)
	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer imageGenerateAgent.runner.Close()
	userMessage := []string{
		"generate imageGenerateAgent black-white logo for trpc-agent-go",
	}
	for _, msg := range userMessage {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			err := imageGenerateAgent.processMessage(ctx, msg)
			if err != nil {
				log.Errorf("Chat system failed to run: %v", err)
			}
		}()
	}

}
