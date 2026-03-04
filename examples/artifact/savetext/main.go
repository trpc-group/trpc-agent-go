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
	ctx := context.Background()
	ns := artifact.Key{
		AppName:   a.appName,
		UserID:    a.userID,
		SessionID: a.sessionID,
	}
	var (
		descs     []artifact.Descriptor
		pageToken string
	)
	for {
		items, next, err := a.artifactService.List(
			ctx,
			ns,
			artifact.WithListLimit(200),
			artifact.WithListPageToken(pageToken),
		)
		if err != nil {
			log.Errorf("Failed to list artifact keys: %v", err)
			break
		}
		descs = append(descs, items...)
		if next == "" {
			break
		}
		pageToken = next
	}
	keys := make([]string, 0, len(descs))
	for _, d := range descs {
		keys = append(keys, d.Key.Name)
	}
	log.Infof("Found %d artifact keys: %v", len(keys), keys)
	if len(descs) != 0 {
		for _, d := range descs {
			data, desc, err := artifact.ReadAll(ctx, a.artifactService, d.Key, &d.Version)
			if err != nil {
				log.Errorf("Failed to load artifact: %v", err)
				continue
			}
			log.Infof(
				"Loaded artifact %s@%s MimeType: %s, Data: %s",
				desc.Key.Name, desc.Version, desc.MimeType, data,
			)

		}

	}

}
