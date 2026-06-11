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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/buildinfo"
)

const (
	releaseTagPrefix = "openclaw-"
)

func currentVersion() string {
	return buildinfo.CurrentVersion()
}

func normalizeReleaseVersion(raw string) string {
	version := strings.TrimSpace(raw)
	version = strings.TrimPrefix(version, releaseTagPrefix)
	if version == "" {
		return ""
	}
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

func releaseTagForVersion(version string) string {
	normalized := normalizeReleaseVersion(version)
	if normalized == "" {
		return ""
	}
	return releaseTagPrefix + normalized
}
