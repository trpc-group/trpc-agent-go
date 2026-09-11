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
	"runtime/debug"
	"strings"
)

const rootModulePath = "trpc.group/trpc-go/trpc-agent-go"

var instrumentVersion = ModuleVersion(rootModulePath)

// InstrumentationVersion returns the version that identifies the
// trpc-agent-go instrumentation scope. It is empty when the build does not
// contain enough information to identify the framework revision reliably.
func InstrumentationVersion() string {
	return instrumentVersion
}

// ModuleVersion returns the version that identifies modulePath as an
// instrumentation scope. It uses the module version for dependencies and the
// VCS revision for an unversioned main module. It returns an empty string when
// the build does not contain enough information to identify the module
// revision reliably.
func ModuleVersion(modulePath string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return moduleVersionFromBuildInfo(info, modulePath)
}

func moduleVersionFromBuildInfo(info *debug.BuildInfo, modulePath string) string {
	if info == nil || modulePath == "" {
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
