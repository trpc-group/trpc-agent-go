//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import "testing"

func TestValidateLocalReadPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "relative file",
			path: "docs/report.txt",
			want: "",
		},
		{
			name: "skill workspace file",
			path: "out/result.txt",
			want: "",
		},
		{
			name: "absolute path",
			path: "/tmp/report.txt",
			want: errAbsolutePath,
		},
		{
			name: "windows absolute path with backslash",
			path: `C:\temp\report.txt`,
			want: errAbsolutePath,
		},
		{
			name: "windows absolute path with slash",
			path: "C:/temp/report.txt",
			want: errAbsolutePath,
		},
		{
			name: "windows unc path",
			path: `\\server\share\report.txt`,
			want: errAbsolutePath,
		},
		{
			name: "parent directory",
			path: "../report.txt",
			want: errPathTraversal,
		},
		{
			name: "parent directory only",
			path: "..",
			want: errPathTraversal,
		},
		{
			name: "parent directory after allowed prefix",
			path: "out/../report.txt",
			want: errPathTraversal,
		},
		{
			name: "parent directory after nested input prefix",
			path: "work/inputs/../report.txt",
			want: errPathTraversal,
		},
		{
			name: "complex traversal multiple levels",
			path: "a/b/../../..",
			want: errPathTraversal,
		},
		{
			name: "traversal with extra slashes",
			path: "a//b/./../..",
			want: errPathTraversal,
		},
		{
			name: "current dir with parent traversal",
			path: "a/./b/../..",
			want: errPathTraversal,
		},
		{
			name: "current directory only",
			path: ".",
			want: "",
		},
		{
			name: "ellipsis path",
			path: "...",
			want: "",
		},
		{
			name: "empty after trim",
			path: "   ",
			want: errFilePathEmpty,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateLocalReadPath(tt.path); got != tt.want {
				t.Fatalf("validateLocalReadPath() = %q, want %q", got, tt.want)
			}
		})
	}
}
