//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates AG-UI SSE heartbeat keepalive frames.
package main

import (
	"flag"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	modelName         = flag.String("model", "deepseek-v3.2", "OpenAI-compatible model name.")
	isStream          = flag.Bool("stream", true, "Whether to stream the model response.")
	address           = flag.String("address", "127.0.0.1:8080", "Listen address.")
	path              = flag.String("path", "/agui", "HTTP path.")
	heartbeatInterval = flag.Duration("heartbeat", time.Second, "SSE heartbeat interval. Set 0 to disable.")
	waitDuration      = flag.Duration("wait", 5*time.Second, "Duration of the tool quiet period.")
)

func main() {
	flag.Parse()
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(768),
		Temperature: floatPtr(0.1),
		Stream:      *isStream,
	}
	runServer(serverConfig{
		ModelName:         *modelName,
		GenerationConfig:  generationConfig,
		WaitDuration:      *waitDuration,
		Address:           *address,
		Path:              *path,
		HeartbeatInterval: *heartbeatInterval,
	})
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
