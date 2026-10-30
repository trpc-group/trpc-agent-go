//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import "testing"

func TestSyntheticModuleVersion(t *testing.T) {
	tests := []struct {
		name        string
		modulePath  string
		baseVersion string
		want        string
	}{
		{
			name:        "unversioned module path keeps base version",
			modulePath:  "example.com/project/module",
			baseVersion: "v0.0.0",
			want:        "v0.0.0",
		},
		{
			name:        "slash major suffix uses matching version",
			modulePath:  "example.com/project/v3",
			baseVersion: "v0.0.0",
			want:        "v3.0.0",
		},
		{
			name:        "compatible requested version is preserved",
			modulePath:  "example.com/project/v3",
			baseVersion: "v3.2.1",
			want:        "v3.2.1",
		},
		{
			name:        "gopkg in suffix uses matching version",
			modulePath:  "gopkg.in/project.v3",
			baseVersion: "v0.0.0",
			want:        "v3.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := syntheticModuleVersion(tt.modulePath, tt.baseVersion); got != tt.want {
				t.Fatalf("syntheticModuleVersion(%q, %q) = %q, want %q", tt.modulePath, tt.baseVersion, got, tt.want)
			}
		})
	}
}
