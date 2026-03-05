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
	limit := 200
	var (
		items     []artifact.ListItem
		pageToken string
	)
	for {
		req := &artifact.ListRequest{
			AppName:   a.appName,
			UserID:    a.userID,
			SessionID: a.sessionID,
			Limit:     &limit,
		}
		if pageToken != "" {
			req.PageToken = &pageToken
		}
		resp, err := a.artifactService.List(ctx, req)
		if err != nil {
			log.Errorf("Failed to list artifacts: %v", err)
			break
		}
		items = append(items, resp.Items...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.Name)
	}
	log.Infof("Found %d artifact keys: %v", len(keys), keys)
	if len(items) != 0 {
		for _, it := range items {
			v := it.Version
			data, desc, err := artifact.ReadAll(ctx, a.artifactService, &artifact.OpenRequest{
				AppName:   a.appName,
				UserID:    a.userID,
				SessionID: a.sessionID,
				Name:      it.Name,
				Version:   &v,
			})
			if err != nil {
				log.Errorf("Failed to load artifact: %v", err)
				continue
			}
			log.Infof(
				"Loaded artifact %s@%s MimeType: %s, Data: %s",
				it.Name, desc.Version, desc.MimeType, data,
			)

		}

	}

}
