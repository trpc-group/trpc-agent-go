//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates primary/backup model failover with LLMAgent and Runner.
package main

import (
	"flag"
	"log"
)

const (
	appName               = "failover-chat-demo"
	agentName             = "failover-chat-agent"
	defaultPrimaryModel   = "gpt-4o-mini"
	defaultBackupModel    = "deepseek-v4-flash"
	defaultPrimaryBaseURL = "https://api.openai.com/v1"
	defaultBackupBaseURL  = "https://api.deepseek.com/v1"
)

func main() {
	primaryModelName := flag.String("primary-model", defaultPrimaryModel, "Primary model name.")
	backupModelName := flag.String("backup-model", defaultBackupModel, "Backup model name.")
	primaryBaseURL := flag.String("primary-base-url", defaultPrimaryBaseURL, "Primary model base URL.")
	backupBaseURL := flag.String("backup-base-url", defaultBackupBaseURL, "Backup model base URL.")
	streaming := flag.Bool("streaming", true, "Enable streaming mode for responses.")
	flag.Parse()
	chat := &failoverChat{
		config: appConfig{
			primaryModelName: *primaryModelName,
			backupModelName:  *backupModelName,
			primaryBaseURL:   *primaryBaseURL,
			backupBaseURL:    *backupBaseURL,
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
	streaming        bool
}
