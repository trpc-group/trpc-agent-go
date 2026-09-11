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
	"strings"
	"testing"
)

func TestInstrumentationVersionFromBuildInfo(t *testing.T) {
	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{
			name: "released main module",
			info: &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v1.2.3"}},
			want: "v1.2.3",
		},
		{
			name: "development main module revision",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: modulePath, Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "0123456789abcdef"},
				},
			},
			want: "0123456789abcdef",
		},
		{
			name: "modified development main module",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: modulePath, Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "0123456789abcdef"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			want: "0123456789abcdef-dirty",
		},
		{
			name: "released dependency",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"},
				Deps: []*debug.Module{{Path: modulePath, Version: "v0.6.0"}},
			},
			want: "v0.6.0",
		},
		{
			name: "pseudo-version dependency",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"},
				Deps: []*debug.Module{{Path: modulePath, Version: "v0.0.0-20260911010101-abcdef123456"}},
			},
			want: "v0.0.0-20260911010101-abcdef123456",
		},
		{
			name: "versioned replacement",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"},
				Deps: []*debug.Module{{
					Path:    modulePath,
					Version: "v0.6.0",
					Replace: &debug.Module{Path: modulePath, Version: "v0.6.1"},
				}},
			},
			want: "v0.6.1",
		},
		{
			name: "local replacement is unknown",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"},
				Deps: []*debug.Module{{
					Path:    modulePath,
					Version: "v0.6.0",
					Replace: &debug.Module{Path: "../trpc-agent-go"},
				}},
			},
		},
		{
			name: "unrelated build",
			info: &debug.BuildInfo{Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"}},
		},
		{name: "missing build info"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := instrumentationVersionFromBuildInfo(test.info); got != test.want {
				t.Fatalf("instrumentationVersionFromBuildInfo() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDefaultServiceName(t *testing.T) {
	if got := DefaultServiceName(); !strings.HasPrefix(got, "unknown_service:") {
		t.Fatalf("DefaultServiceName() = %q, want unknown_service prefix", got)
	}
}
