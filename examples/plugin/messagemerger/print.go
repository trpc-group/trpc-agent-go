//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func collectFinalResponse(evCh <-chan *event.Event) (string, error) {
	for ev := range evCh {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			return "", fmt.Errorf("runner event error: %s", ev.Error.Message)
		}
		if !ev.IsFinalResponse() || len(ev.Choices) == 0 {
			continue
		}
		return strings.TrimSpace(ev.Choices[0].Message.Content), nil
	}
	return "", fmt.Errorf("no final response received")
}

func printMessages(title string, messages []model.Message) {
	fmt.Println(title + ":")
	if len(messages) == 0 {
		fmt.Println("  <empty>")
		return
	}
	for i, msg := range messages {
		fmt.Printf("  %d. role=%s content=%q\n", i, msg.Role, msg.Content)
	}
}
