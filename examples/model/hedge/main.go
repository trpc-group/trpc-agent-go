//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates hedged model requests with LLMAgent and Runner.
package main

import (
	"flag"
	"log"
	"time"
)

const (
	appName               = "hedge-chat-demo"
	agentName             = "hedge-chat-agent"
	defaultPrimaryModel   = "gpt-4o-mini"
	defaultBackupModel    = "deepseek-chat"
	defaultPrimaryBaseURL = "https://api.openai.com/v1"
	defaultBackupBaseURL  = "https://api.deepseek.com/v1"
	defaultHedgeDelay     = 100 * time.Millisecond
)

func main() {
	primaryModelName := flag.String("primary-model", defaultPrimaryModel, "Primary model name.")
	backupModelName := flag.String("backup-model", defaultBackupModel, "Backup model name.")
	primaryBaseURL := flag.String("primary-base-url", defaultPrimaryBaseURL, "Primary model base URL.")
	backupBaseURL := flag.String("backup-base-url", defaultBackupBaseURL, "Backup model base URL.")
	hedgeDelay := flag.Duration("hedge-delay", defaultHedgeDelay, "Delay before launching the next hedge candidate.")
	streaming := flag.Bool("streaming", true, "Enable streaming mode for responses.")
	flag.Parse()
	chat := &hedgeChat{
		config: appConfig{
			primaryModelName: *primaryModelName,
			backupModelName:  *backupModelName,
			primaryBaseURL:   *primaryBaseURL,
			backupBaseURL:    *backupBaseURL,
			hedgeDelay:       *hedgeDelay,
			streaming:        *streaming,
		},
	}
	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

type appConfig struct {
	primaryModelName string
	backupModelName  string
	primaryBaseURL   string
	backupBaseURL    string
	hedgeDelay       time.Duration
	streaming        bool
}
