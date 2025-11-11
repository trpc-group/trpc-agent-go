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

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

func main() {
	// Parse command line arguments
	modelName := flag.String("model", "deepseek-chat", "Model name to use")
	flag.Parse()
	a := newLogQueryAgent("log_app", "log_agent", *modelName)
	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer a.runner.Close()
	userMessage := []string{
		"Calculate 123 + 456 * 789",
		//"What day of the week is today?",
		//"'Hello World' to uppercase",
		//"Create a test file in the current directory",
		//"Find information about Tesla company",
	}

	for _, msg := range userMessage {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			err := a.processMessage(ctx, msg)
			if err != nil {
				log.Errorf("Chat system failed to run: %v", err)
			}
		}()
	}
	keys, err := a.artifactService.ListArtifactKeys(context.Background(),
		artifact.SessionInfo{AppName: a.appName, UserID: a.userID, SessionID: a.sessionID})
	if err != nil {
		log.Errorf("Failed to list artifact keys: %v", err)
	}
	log.Infof("Found %d artifact keys: %v", len(keys), keys)
	if len(keys) != 0 {
		for _, key := range keys {
			a, err := a.artifactService.LoadArtifact(context.Background(),
				artifact.SessionInfo{AppName: a.appName, UserID: a.userID, SessionID: a.sessionID}, key, nil)
			if err != nil {
				log.Errorf("Failed to load artifact: %v", err)
			}
			log.Infof("Loaded artifact MimeType: %s, Data: %s", a.MimeType, a.Data)

		}

	}

}
