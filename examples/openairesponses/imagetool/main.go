//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a ReadImage tool whose image is sent to the main
// model as a native Responses function-call output.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	imageID       = "sample"
	readImageName = "read_image"
	maxImageBytes = 10 << 20
)

type readImageArgs struct {
	ImageID string `json:"image_id" jsonschema:"description=The image identifier. Use sample for the configured image."`
}

type readImageResult struct {
	// Keep binary data out of the default JSON text. The callback receives
	// this original value and puts the bytes into an image content part.
	Data   []byte `json:"-"`
	Format string `json:"format"`
}

func main() {
	modelName := flag.String("model", "gpt-5", "Responses model that supports images in tool outputs")
	filename := flag.String("image", "", "PNG, JPEG, static GIF, or WebP file to expose as image ID sample")
	prompt := flag.String("prompt", "Read the visible text and describe the image.", "Question to answer using the image")
	flag.Parse()
	if err := run(context.Background(), *modelName, *filename, *prompt); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, modelName, filename, prompt string) error {
	if filename == "" {
		return fmt.Errorf("-image is required")
	}
	readImage := function.NewFunctionTool(
		func(ctx context.Context, args readImageArgs) (*readImageResult, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if args.ImageID != imageID {
				return nil, fmt.Errorf("unknown image ID %q", args.ImageID)
			}
			// Applications can replace this lookup with their own image-ID
			// resolver or authenticated download.
			return loadImage(filename)
		},
		function.WithName(readImageName),
		function.WithDescription("Read image sample so you can inspect it directly."),
	)
	callbacks := tool.NewCallbacks().RegisterToolResultMessages(imageResultMessages)
	imageAgent := llmagent.New(
		"responses-image-agent",
		llmagent.WithModel(responses.New(modelName)),
		llmagent.WithInstruction("Call read_image with image_id sample before answering. Answer using the returned image."),
		llmagent.WithTools([]tool.Tool{readImage}),
		llmagent.WithToolCallbacks(callbacks),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: false}),
	)
	r := runner.NewRunner("responses-image-example", imageAgent)
	defer r.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := r.Run(ctx, "demo-user", "image-session", model.NewUserMessage(prompt))
	if err != nil {
		return err
	}
	for ev := range events {
		if ev.Error != nil {
			return fmt.Errorf("agent: %s", ev.Error.Message)
		}
		if ev.IsFinalResponse() {
			for _, choice := range ev.Choices {
				if choice.Message.Role == model.RoleAssistant {
					fmt.Println(choice.Message.Content)
				}
			}
		}
	}
	return nil
}

func loadImage(filename string) (*readImageResult, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", maxImageBytes)
	}
	mime := http.DetectContentType(data)
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return nil, fmt.Errorf("unsupported image type %q", mime)
	}
	return &readImageResult{Data: data, Format: mime}, nil
}

func imageResultMessages(_ context.Context, in *tool.ToolResultMessagesInput) (any, error) {
	if in.ToolName != readImageName {
		return nil, nil
	}
	result, ok := in.Result.(*readImageResult)
	if !ok || result == nil {
		// Preserve normal framework error results and substitutions.
		return nil, nil
	}
	msg, ok := in.DefaultToolMessage.(model.Message)
	if !ok {
		return nil, fmt.Errorf("unexpected default tool message type %T", in.DefaultToolMessage)
	}
	msg.Content = "Image sample."
	msg.AddImageData(result.Data, "auto", result.Format)
	return msg, nil
}
