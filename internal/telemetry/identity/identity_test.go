//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package identity

import (
	"runtime/debug"
	"testing"
)

func TestModuleVersionFromBuildInfo(t *testing.T) {
	tests := []struct {
		name       string
		modulePath string
		info       *debug.BuildInfo
		want       string
	}{
		{
			name:       "released main module",
			modulePath: rootModulePath,
			info:       &debug.BuildInfo{Main: debug.Module{Path: rootModulePath, Version: "v1.2.3"}},
			want:       "v1.2.3",
		},
		{
			name:       "development main module revision",
			modulePath: rootModulePath,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: rootModulePath, Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "0123456789abcdef"},
				},
			},
			want: "0123456789abcdef",
		},
		{
			name:       "modified development main module",
			modulePath: rootModulePath,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: rootModulePath, Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "0123456789abcdef"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			want: "0123456789abcdef-dirty",
		},
		{
			name:       "released dependency",
			modulePath: rootModulePath,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"},
				Deps: []*debug.Module{{Path: rootModulePath, Version: "v0.6.0"}},
			},
			want: "v0.6.0",
		},
		{
			name:       "pseudo-version dependency",
			modulePath: rootModulePath,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"},
				Deps: []*debug.Module{{Path: rootModulePath, Version: "v0.0.0-20260911010101-abcdef123456"}},
			},
			want: "v0.0.0-20260911010101-abcdef123456",
		},
		{
			name:       "versioned replacement",
			modulePath: rootModulePath,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"},
				Deps: []*debug.Module{{
					Path:    rootModulePath,
					Version: "v0.6.0",
					Replace: &debug.Module{Path: rootModulePath, Version: "v0.6.1"},
				}},
			},
			want: "v0.6.1",
		},
		{
			name:       "local replacement is unknown",
			modulePath: rootModulePath,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"},
				Deps: []*debug.Module{{
					Path:    rootModulePath,
					Version: "v0.6.0",
					Replace: &debug.Module{Path: "../trpc-agent-go"},
				}},
			},
		},
		{
			name:       "independently versioned module",
			modulePath: rootModulePath + "/session/redis",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"},
				Deps: []*debug.Module{
					{Path: rootModulePath, Version: "v0.6.0"},
					{Path: rootModulePath + "/session/redis", Version: "v0.0.3"},
				},
			},
			want: "v0.0.3",
		},
		{
			name:       "unrelated build",
			modulePath: rootModulePath,
			info:       &debug.BuildInfo{Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"}},
		},
		{name: "missing build info", modulePath: rootModulePath},
		{name: "missing module path", info: &debug.BuildInfo{Main: debug.Module{Path: rootModulePath}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := moduleVersionFromBuildInfo(test.info, test.modulePath); got != test.want {
				t.Fatalf("moduleVersionFromBuildInfo() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestInstrumentationVersion(t *testing.T) {
	if got := InstrumentationVersion(); got != instrumentVersion {
		t.Fatalf("InstrumentationVersion() = %q, want %q", got, instrumentVersion)
	}
}
