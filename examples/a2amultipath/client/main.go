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
	"context"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
)

const (
	defaultAgentURL = "http://localhost:8888/agents/math"
	defaultMessage  = "hello"
)

func main() {
	agentURL := flag.String("url", defaultAgentURL, "A2A agent base URL")
	message := flag.String("msg", defaultMessage, "User message")
	flag.Parse()

	c, err := client.NewA2AClient(*agentURL)
	if err != nil {
		log.Fatalf("create client: %v", err)
	}

	msg := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{
			protocol.NewTextPart(*message),
		},
	)

	rsp, err := c.SendMessage(
		context.Background(),
		protocol.SendMessageParams{Message: msg},
	)
	if err != nil {
		log.Fatalf("send message: %v", err)
	}
	printMessageResult(rsp)
}

func printMessageResult(rsp *protocol.MessageResult) {
	if rsp == nil || rsp.Result == nil {
		fmt.Println("empty response")
		return
	}

	switch v := rsp.Result.(type) {
	case *protocol.Message:
		printTextParts(v.Parts)
	case *protocol.Task:
		printTask(v)
	default:
		fmt.Printf("unexpected result type: %T\n", rsp.Result)
	}
}

func printTask(task *protocol.Task) {
	if task == nil {
		fmt.Println("empty task")
		return
	}
	for _, msg := range task.History {
		printTextParts(msg.Parts)
	}
	for _, art := range task.Artifacts {
		printTextParts(art.Parts)
	}
}

func printTextParts(parts []protocol.Part) {
	for _, part := range parts {
		switch part.GetKind() {
		case protocol.KindText:
			textPart := asTextPart(part)
			if textPart == nil {
				continue
			}
			fmt.Print(textPart.Text)
		}
	}
	fmt.Println()
}

func asTextPart(part protocol.Part) *protocol.TextPart {
	if part == nil {
		return nil
	}
	if p, ok := part.(*protocol.TextPart); ok {
		return p
	}
	if p, ok := part.(protocol.TextPart); ok {
		return &p
	}
	return nil
}
