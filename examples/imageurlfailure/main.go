//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates continuing a session after a model-side image URL
// failure blocks an earlier turn.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName = flag.String(
		"model",
		os.Getenv("MODEL_NAME"),
		"Name of the model to use (default: MODEL_NAME env var)",
	)
	baseURL = flag.String(
		"base-url",
		os.Getenv("OPENAI_BASE_URL"),
		"OpenAI-compatible base URL (default: OPENAI_BASE_URL env var)",
	)
	apiKey = flag.String(
		"api-key",
		os.Getenv("OPENAI_API_KEY"),
		"API key for the model service (default: OPENAI_API_KEY env var)",
	)
	imageURL = flag.String(
		"image-url",
		"https://example.invalid/unavailable.png",
		"Image URL to attach to the first user message",
	)
)

func main() {
	flag.Parse()
	if *modelName == "" {
		log.Fatal("model name is required; set MODEL_NAME or pass -model")
	}

	fmt.Println("Image URL failure continuation demo")
	fmt.Printf("Model: %s\n", *modelName)
	if *baseURL != "" {
		fmt.Printf("Base URL: %s\n", *baseURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	modelOpts := []openai.Option{}
	if *baseURL != "" {
		modelOpts = append(modelOpts, openai.WithBaseURL(*baseURL))
	}
	if *apiKey != "" {
		modelOpts = append(modelOpts, openai.WithAPIKey(*apiKey))
	}

	agent := llmagent.New(
		"image-url-failure-agent",
		llmagent.WithModel(openai.New(*modelName, modelOpts...)),
		llmagent.WithInstruction(
			"If an earlier image is unavailable, say that the image content was not observed and continue with text-only help.",
		),
		llmagent.WithImageURLFailureContinuation(true),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: false}),
	)
	r := runner.NewRunner(
		"image-url-failure-demo",
		agent,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer r.Close()

	sessionID := "image-url-failure-session"
	first := model.NewUserMessage("Describe this image briefly.")
	first.AddImageURL(*imageURL, "auto")

	fmt.Println("First turn: sending image URL")
	if _, err := runTurn(ctx, r, sessionID, first); err != nil {
		fmt.Printf("First turn failed as expected for an unavailable image URL: %v\n", err)
	} else {
		fmt.Println("First turn succeeded; the provider accepted the image URL.")
	}

	fmt.Println("Second turn: continuing in the same session")
	second := model.NewUserMessage("Can we continue without relying on that image?")
	text, err := runTurn(ctx, r, sessionID, second)
	if err != nil {
		log.Fatalf("second turn failed: %v", err)
	}
	fmt.Printf("Assistant: %s\n", text)
}

func runTurn(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
	msg model.Message,
) (string, error) {
	events, err := r.Run(ctx, "demo-user", sessionID, msg)
	if err != nil {
		return "", err
	}
	return collectResponse(events)
}

func collectResponse(events <-chan *event.Event) (string, error) {
	var out string
	for evt := range events {
		if evt.Error != nil {
			return out, errors.New(evt.Error.Message)
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		out += choice.Delta.Content
		out += choice.Message.Content
		if evt.IsFinalResponse() {
			return out, nil
		}
	}
	return out, nil
}
