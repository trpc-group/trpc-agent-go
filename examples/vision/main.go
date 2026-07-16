//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates giving a text-only main model image understanding
// through a separate vision model and the analyze_image tool.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/vision"
)

var (
	imagePath = flag.String("image", "", "Path to the image to analyze")
	prompt    = flag.String(
		"prompt",
		"Describe the image and transcribe its important visible text.",
		"Question or instruction about the image",
	)
	mainModelName = flag.String(
		"main-model",
		envOrDefault("MAIN_MODEL_NAME", "gpt-4.1-mini"),
		"Text-only main model name",
	)
	visionModelName = flag.String(
		"vision-model",
		envOrDefault("VISION_MODEL_NAME", "gpt-4.1-mini"),
		"Vision model name",
	)
	mainVariant = flag.String(
		"main-variant",
		envOrDefault("MAIN_OPENAI_VARIANT", string(openai.VariantOpenAI)),
		"Main model OpenAI adapter variant",
	)
	visionVariant = flag.String(
		"vision-variant",
		envOrDefault("VISION_OPENAI_VARIANT", string(openai.VariantOpenAI)),
		"Vision model OpenAI adapter variant",
	)
)

func main() {
	flag.Parse()
	if strings.TrimSpace(*imagePath) == "" {
		log.Fatal("-image is required")
	}
	if strings.TrimSpace(*prompt) == "" {
		log.Fatal("-prompt must not be empty")
	}

	mainModel, err := newOpenAIModel(modelConfig{
		name:     *mainModelName,
		baseURL:  firstNonEmpty(os.Getenv("MAIN_OPENAI_BASE_URL"), os.Getenv("OPENAI_BASE_URL")),
		apiKey:   firstNonEmpty(os.Getenv("MAIN_OPENAI_API_KEY"), os.Getenv("OPENAI_API_KEY")),
		variant:  *mainVariant,
		textOnly: true,
	})
	if err != nil {
		log.Fatalf("Create main model: %v", err)
	}
	visionModel, err := newOpenAIModel(modelConfig{
		name:    *visionModelName,
		baseURL: firstNonEmpty(os.Getenv("VISION_OPENAI_BASE_URL"), os.Getenv("OPENAI_BASE_URL")),
		apiKey:  firstNonEmpty(os.Getenv("VISION_OPENAI_API_KEY"), os.Getenv("OPENAI_API_KEY")),
		variant: *visionVariant,
	})
	if err != nil {
		log.Fatalf("Create vision model: %v", err)
	}

	imageTool, err := vision.New(visionModel)
	if err != nil {
		log.Fatalf("Create vision tool: %v", err)
	}
	agent := llmagent.New(
		"vision-tool-assistant",
		llmagent.WithModel(mainModel),
		llmagent.WithDescription(
			"A text assistant that uses a separate vision model to inspect images.",
		),
		llmagent.WithInstruction(
			"When the user attaches images or asks about their visual content, call "+
				"analyze_image. For images attached to the current message, omit "+
				"image_urls and pass only a focused prompt. Do not claim that you "+
				"cannot see an image before using the tool.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: false}),
		llmagent.WithTools([]tool.Tool{imageTool}),
	)

	r := runner.NewRunner("vision-tool-example", agent)
	defer r.Close()

	message := model.NewUserMessage(*prompt)
	if err := message.AddImageFilePath(*imagePath, "auto"); err != nil {
		log.Fatalf("Attach image: %v", err)
	}

	fmt.Printf("Main model: %s (text-only request payload)\n", *mainModelName)
	fmt.Printf("Vision model: %s\n", *visionModelName)
	fmt.Printf("Image: %s\n", *imagePath)
	fmt.Printf("Prompt: %s\n\n", *prompt)

	events, err := r.Run(
		context.Background(),
		"example-user",
		fmt.Sprintf("vision-example-%d", time.Now().UnixNano()),
		message,
	)
	if err != nil {
		log.Fatalf("Run agent: %v", err)
	}
	if err := printEvents(events); err != nil {
		log.Fatalf("Agent failed: %v", err)
	}
}

type modelConfig struct {
	name     string
	baseURL  string
	apiKey   string
	variant  string
	textOnly bool
}

func newOpenAIModel(cfg modelConfig) (*openai.Model, error) {
	if strings.TrimSpace(cfg.name) == "" {
		return nil, fmt.Errorf("model name is required")
	}
	if strings.TrimSpace(cfg.apiKey) == "" {
		return nil, fmt.Errorf("API key is required")
	}
	variant, err := parseVariant(cfg.variant)
	if err != nil {
		return nil, err
	}
	opts := []openai.Option{
		openai.WithAPIKey(cfg.apiKey),
		openai.WithVariant(variant),
	}
	if strings.TrimSpace(cfg.baseURL) != "" {
		opts = append(opts, openai.WithBaseURL(cfg.baseURL))
	}
	if cfg.textOnly {
		opts = append(opts, openai.WithTextOnlyMessageContent(true))
	}
	return openai.New(cfg.name, opts...), nil
}

func parseVariant(value string) (openai.Variant, error) {
	switch openai.Variant(strings.ToLower(strings.TrimSpace(value))) {
	case openai.VariantOpenAI:
		return openai.VariantOpenAI, nil
	case openai.VariantHunyuan:
		return openai.VariantHunyuan, nil
	case openai.VariantDeepSeek:
		return openai.VariantDeepSeek, nil
	case openai.VariantQwen:
		return openai.VariantQwen, nil
	case openai.VariantGLM:
		return openai.VariantGLM, nil
	default:
		return "", fmt.Errorf("unsupported OpenAI adapter variant %q", value)
	}
}

func printEvents(events <-chan *event.Event) error {
	var (
		final           string
		visionCallSeen  bool
		visionSucceeded bool
		lastVisionError string
	)
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return fmt.Errorf("%s", evt.Error.Message)
		}
		if evt.Response == nil || len(evt.Choices) == 0 {
			continue
		}

		choice := evt.Choices[0]
		for _, call := range choice.Message.ToolCalls {
			fmt.Printf("Tool call: %s\n", call.Function.Name)
			if call.Function.Name == vision.ToolName {
				visionCallSeen = true
			}
		}
		if choice.Message.Role == model.RoleTool &&
			choice.Message.ToolName == vision.ToolName {
			fmt.Printf("Vision result: %s\n\n", choice.Message.Content)
			if strings.HasPrefix(strings.TrimSpace(choice.Message.Content), "Error:") {
				lastVisionError = choice.Message.Content
			} else {
				visionSucceeded = true
			}
		}
		if evt.IsFinalResponse() {
			final = choice.Message.Content
		}
	}
	if !visionCallSeen {
		return fmt.Errorf("main model did not call %s", vision.ToolName)
	}
	if !visionSucceeded {
		return fmt.Errorf("vision tool did not succeed: %s", lastVisionError)
	}
	if strings.TrimSpace(final) == "" {
		return fmt.Errorf("main model returned no final response")
	}
	fmt.Printf("Assistant: %s\n", final)
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
