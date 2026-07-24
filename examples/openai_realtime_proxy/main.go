//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main runs a text-capable OpenAI Realtime WebSocket proxy.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/log"
	modelrealtime "trpc.group/trpc-go/trpc-agent-go/model/openai/realtime"
	serverrealtime "trpc.group/trpc-go/trpc-agent-go/server/openai/realtime"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	modelName := flag.String("model", "gpt-realtime", "OpenAI Realtime model")
	flag.Parse()

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}
	upstreamURL := fmt.Sprintf(
		"wss://api.openai.com/v1/realtime?model=%s",
		url.QueryEscape(*modelName),
	)
	proxy, err := serverrealtime.New(
		serverrealtime.WithUpstream(modelrealtime.Config{
			URL:    upstreamURL,
			APIKey: apiKey,
		}),
	)
	if err != nil {
		log.Fatalf("create OpenAI Realtime proxy: %v", err)
	}

	log.Infof(
		"OpenAI Realtime proxy listening on ws://localhost%s%s",
		*addr,
		proxy.Path(),
	)
	// This example server intentionally uses the standard HTTP server.
	//nolint:gosec
	if err := http.ListenAndServe(*addr, proxy.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
