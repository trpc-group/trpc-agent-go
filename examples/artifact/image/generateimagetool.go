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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type generateImageInput struct {
	Prompt string `json:"prompt"`
}

type generateImageOutput struct {
	Result string `json:"result"`
}

func generateImage(ctx context.Context, input generateImageInput) (generateImageOutput, error) {
	images, err := mockLLMGenerateImage(ctx, input.Prompt)
	if err != nil {
		return generateImageOutput{}, fmt.Errorf("failed to generate image: %w", err)
	}
	if len(images) == 0 {
		return generateImageOutput{"No images generated"}, nil
	}
	toolCtx, err := agent.NewToolContext(ctx)
	if err != nil {
		return generateImageOutput{}, fmt.Errorf("failed to create tool context: %w", err)
	}
	var output generateImageOutput

	var stateValue generateImageStateValue
	for _, img := range images {
		output.Result += fmt.Sprintf("Image has been generated at the URL %s.", img.url)
		a := &artifact.Artifact{
			MimeType: img.mimeType,
			URL:      img.url,
			Data:     img.content,
		}
		imageID := generateRandomID()
		_, err = toolCtx.SaveArtifact(imageID, a)
		if err != nil {
			output.Result += fmt.Sprintf("Failed to save image(%s) to artifact, err: %v\n", img.url, err)
		} else {
			stateValue.ImageIDs = append(stateValue.ImageIDs, imageID)
			output.Result += fmt.Sprintf(" Saved as artifact with ID: %s\n", imageID)
		}
	}

	bts, err := json.Marshal(stateValue)
	if err != nil {
		return generateImageOutput{}, fmt.Errorf("failed to marshal state: %w", err)
	}
	toolCtx.State[generateImageStateKey] = bts
	return output, nil
}

type image struct {
	mimeType string
	content  []byte
	url      string
}

func mockLLMGenerateImage(ctx context.Context, prompt string) ([]image, error) {
	// Get the current directory
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	// Resource directory path
	resourceDir := filepath.Join(currentDir, "resource")

	// Read directory contents
	files, err := os.ReadDir(resourceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource directory: %w", err)
	}

	var images []image

	// Process each image file
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// Check if it's an image file
		fileName := file.Name()
		if !strings.HasSuffix(strings.ToLower(fileName), ".png") &&
			!strings.HasSuffix(strings.ToLower(fileName), ".jpg") &&
			!strings.HasSuffix(strings.ToLower(fileName), ".jpeg") {
			continue
		}

		// Read file content
		filePath := filepath.Join(resourceDir, fileName)
		content, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Printf("Warning: failed to read file %s: %v\n", fileName, err)
			continue
		}

		// Determine MIME type based on file extension
		var mimeType string
		ext := strings.ToLower(filepath.Ext(fileName))
		switch ext {
		case ".png":
			mimeType = "image/png"
		case ".jpg", ".jpeg":
			mimeType = "image/jpeg"
		default:
			mimeType = "image/png" // default to PNG
		}

		// Create image struct
		img := image{
			mimeType: mimeType,
			content:  content,
			url:      fmt.Sprintf("file://%s", filePath),
		}

		images = append(images, img)
	}
	return images, nil
}

var generateImageTool = function.NewFunctionTool(
	generateImage,
	function.WithName("text-to-image"),
	function.WithDescription("generate image by input text"),
)
