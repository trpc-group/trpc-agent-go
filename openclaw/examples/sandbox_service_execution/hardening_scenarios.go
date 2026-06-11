//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	metadataProtectionMarker    = "OPENCLAW_SERVICE_METADATA_PROTECTION_OK"
	sessionIDSanitizationMarker = "OPENCLAW_SERVICE_SESSION_ID_SANITIZATION_OK"
	sessionIDCollisionMarker    = "OPENCLAW_SERVICE_SESSION_ID_SANITIZATION_COLLISION"
)

func runMetadataProtection(ctx context.Context, cfg exampleConfig, requireOSSandbox bool) error {
	svc, err := startScenarioService(ctx, cfg, "metadata-protection", requireOSSandbox)
	if err != nil {
		return err
	}
	defer svc.close()
	text, err := svc.runPrompt(
		ctx,
		"metadata-protection",
		codePrompt(
			"try to write the text bad to ../.git/config. "+
				"If the write succeeds, print OPENCLAW_SERVICE_METADATA_PROTECTION_UNEXPECTED_WRITE. "+
				"If the write fails, print "+metadataProtectionMarker+" and the exception type.",
		),
	)
	if err != nil {
		return err
	}
	if strings.Contains(text, "OPENCLAW_SERVICE_METADATA_PROTECTION_UNEXPECTED_WRITE") {
		return errors.New("protected metadata write unexpectedly succeeded")
	}
	return requireContainsAll(text, metadataProtectionMarker)
}

func runSessionIDSanitization(ctx context.Context, cfg exampleConfig, requireOSSandbox bool) error {
	svc, err := startScenarioService(ctx, cfg, "session-id-sanitization", requireOSSandbox)
	if err != nil {
		return err
	}
	defer svc.close()
	_, err = svc.runPrompt(
		ctx,
		"user:a",
		codePrompt(
			"write "+sessionIDSanitizationMarker+" to session_marker.txt "+
				"in the current working directory and print WROTE.",
		),
	)
	if err != nil {
		return err
	}
	text, err := svc.runPrompt(
		ctx,
		"user_a",
		codePrompt(
			"check whether session_marker.txt exists in the current working directory. "+
				"If it exists, print "+sessionIDCollisionMarker+". "+
				"If it does not exist, print "+sessionIDSanitizationMarker+".",
		),
	)
	if err != nil {
		return err
	}
	if strings.Contains(text, sessionIDCollisionMarker) {
		return fmt.Errorf("session IDs collided: %s", trim(text))
	}
	return requireContainsAll(text, sessionIDSanitizationMarker)
}
