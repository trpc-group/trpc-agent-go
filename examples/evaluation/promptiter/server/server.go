//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"net/http"

	spromptiter "trpc.group/trpc-go/trpc-agent-go/server/promptiter"
)

func runPromptIterServer(ctx context.Context, cfg serverConfig) error {
	runtime, err := buildPromptIterRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	defer runtime.close()
	server, err := spromptiter.New(
		spromptiter.WithAppName(appName),
		spromptiter.WithBasePath(cfg.BasePath),
		spromptiter.WithEngine(runtime.engine),
		spromptiter.WithManager(runtime.manager),
	)
	if err != nil {
		return fmt.Errorf("create promptiter server: %w", err)
	}
	return http.ListenAndServe(cfg.Addr, server.Handler())
}
