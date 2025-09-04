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
	imageGenerateAgent := newImageGenerateAgent("image_app", "image_generate_agent", *modelName)
	userMessage := []string{
		"generate imageGenerateAgent black-white logo for trpc-agent-go",
	}

	sessionInfo := artifact.SessionInfo{AppName: imageGenerateAgent.appName, UserID: imageGenerateAgent.userID, SessionID: imageGenerateAgent.sessionID}
	for _, msg := range userMessage {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			err := imageGenerateAgent.processMessage(ctx, msg)
			if err != nil {
				log.Errorf("Chat system failed to run: %v", err)
			}
			keys, err := imageGenerateAgent.artifactService.ListArtifactKeys(context.Background(), sessionInfo)
			if err != nil {
				log.Errorf("Failed to list artifact keys: %v", err)
			}
			log.Infof("Found %d artifact keys: %v", len(keys), keys)
			if len(keys) != 0 {
				for _, key := range keys {
					a, err := imageGenerateAgent.artifactService.LoadArtifact(context.Background(), sessionInfo, key, nil)
					if err != nil {
						log.Errorf("Failed to load artifact: %v", err)
					}
					log.Infof("Loaded artifact MimeType: %s, URL: %s", a.MimeType, a.URL)
					if err := imageGenerateAgent.artifactService.DeleteArtifact(context.Background(), sessionInfo, key); err != nil {
						log.Errorf("Failed to delete artifact: %v", err)
					} else {
						log.Infof("Delete artifact MimeType: %s, URL: %s", a.MimeType, a.URL)
					}
				}
			}
		}()
	}

}
