//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package identity provides the OpenTelemetry identity of trpc-agent-go.
package identity

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

const modulePath = "trpc.group/trpc-go/trpc-agent-go"

var instrumentVersion = detectInstrumentationVersion()

// InstrumentationVersion returns the version that identifies the
// trpc-agent-go instrumentation scope. It is empty when the build does not
// contain enough information to identify the framework revision reliably.
func InstrumentationVersion() string {
	return instrumentVersion
}

// DefaultServiceName returns the OpenTelemetry fallback service name for the
// current executable.
func DefaultServiceName() string {
	executable, err := os.Executable()
	if err != nil {
		return "unknown_service:go"
	}
	return "unknown_service:" + filepath.Base(executable)
}

func detectInstrumentationVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return instrumentationVersionFromBuildInfo(info)
}

func instrumentationVersionFromBuildInfo(info *debug.BuildInfo) string {
	if info == nil {
		return ""
	}
	if info.Main.Path == modulePath {
		if version := usableModuleVersion(&info.Main); version != "" {
			return version
		}
		return mainModuleRevision(info.Settings)
	}
	for _, dependency := range info.Deps {
		if dependency.Path == modulePath {
			return usableModuleVersion(dependency)
		}
	}
	return ""
}

func usableModuleVersion(module *debug.Module) string {
	if module == nil {
		return ""
	}
	if module.Replace != nil {
		return usableVersion(module.Replace.Version)
	}
	return usableVersion(module.Version)
}

func usableVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "(devel)" {
		return ""
	}
	return version
}

func mainModuleRevision(settings []debug.BuildSetting) string {
	var revision string
	modified := false
	for _, setting := range settings {
		switch setting.Key {
		case "vcs.revision":
			revision = strings.TrimSpace(setting.Value)
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision != "" && modified {
		return revision + "-dirty"
	}
	return revision
}
