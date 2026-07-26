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
	"net"
	"net/http"
	"net/url"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/log"
	modelrealtime "trpc.group/trpc-go/trpc-agent-go/model/openai/realtime"
	serverrealtime "trpc.group/trpc-go/trpc-agent-go/server/openai/realtime"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "loopback listen address")
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
	defer proxy.Close()

	if !isLoopbackListenAddress(*addr) {
		log.Fatal(
			"this example only accepts a loopback -addr; wrap proxy.Handler() " +
				"with authentication and serve it with TLS for remote access",
		)
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen on %s: %v", *addr, err)
	}
	defer listener.Close()

	log.Infof("OpenAI Realtime proxy listening on ws://%s%s", listener.Addr(), proxy.Path())
	// This example server intentionally uses the standard HTTP server.
	//nolint:gosec
	if err := http.Serve(listener, proxy.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func isLoopbackListenAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
