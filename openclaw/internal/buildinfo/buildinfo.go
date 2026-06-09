//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package buildinfo exposes OpenClaw binary build metadata.
package buildinfo

import (
	"runtime"
	runtimedebug "runtime/debug"
	"strings"
)

const (
	// DefaultReleaseVersion is used when a development binary is not built
	// through the release pipeline.
	DefaultReleaseVersion = "dev"

	buildSettingVCSRevision = "vcs.revision"
	buildSettingVCSTime     = "vcs.time"
	buildSettingVCSModified = "vcs.modified"
)

// ReleaseVersion is set by release builds with -ldflags.
var ReleaseVersion = DefaultReleaseVersion

// SourceCommit is set by release builds with -ldflags.
var SourceCommit string

// Info is the runtime build metadata written to debug bundles.
type Info struct {
	Version       string `json:"version"`
	SourceCommit  string `json:"source_commit,omitempty"`
	GoVersion     string `json:"go_version,omitempty"`
	ModulePath    string `json:"module_path,omitempty"`
	ModuleVersion string `json:"module_version,omitempty"`
	VCSRevision   string `json:"vcs_revision,omitempty"`
	VCSTime       string `json:"vcs_time,omitempty"`
	VCSModified   bool   `json:"vcs_modified,omitempty"`
}

// CurrentVersion returns the release version visible to users.
func CurrentVersion() string {
	version := strings.TrimSpace(ReleaseVersion)
	if version == "" {
		return DefaultReleaseVersion
	}
	return version
}

// Snapshot returns best-effort build metadata for diagnostics.
func Snapshot() Info {
	info := Info{
		Version:      CurrentVersion(),
		SourceCommit: strings.TrimSpace(SourceCommit),
		GoVersion:    runtime.Version(),
	}
	if build, ok := runtimedebug.ReadBuildInfo(); ok && build != nil {
		info.ModulePath = strings.TrimSpace(build.Main.Path)
		info.ModuleVersion = strings.TrimSpace(build.Main.Version)
		applyBuildSettings(&info, build.Settings)
	}
	if info.SourceCommit == "" {
		info.SourceCommit = info.VCSRevision
	}
	return info
}

func applyBuildSettings(info *Info, settings []runtimedebug.BuildSetting) {
	if info == nil {
		return
	}
	for _, setting := range settings {
		value := strings.TrimSpace(setting.Value)
		switch setting.Key {
		case buildSettingVCSRevision:
			info.VCSRevision = value
		case buildSettingVCSTime:
			info.VCSTime = value
		case buildSettingVCSModified:
			info.VCSModified = value == "true"
		}
	}
}
