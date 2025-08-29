//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

func TestFileHasUserNamespace(t *testing.T) {
	tests := []struct {
		filename string
		expected bool
	}{
		{"user:test.txt", true},
		{"user:document.pdf", true},
		{"user:", true},
		{"regular_file.txt", false},
		{"test.txt", false},
		{"userfile.txt", false}, // doesn't start with "user:"
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := FileHasUserNamespace(tt.filename)
			if result != tt.expected {
				t.Errorf("FileHasUserNamespace(%q) = %v, want %v", tt.filename, result, tt.expected)
			}
		})
	}
}

func TestBuildArtifactPath(t *testing.T) {
	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	tests := []struct {
		name     string
		filename string
		expected string
	}{
		{
			name:     "regular file",
			filename: "test.txt",
			expected: "testapp/user123/session456/test.txt",
		},
		{
			name:     "user namespaced file",
			filename: "user:document.pdf",
			expected: "testapp/user123/user/user:document.pdf",
		},
		{
			name:     "empty filename",
			filename: "",
			expected: "testapp/user123/session456/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildArtifactPath(sessionInfo, tt.filename)
			if result != tt.expected {
				t.Errorf("BuildArtifactPath() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestBuildObjectName(t *testing.T) {
	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	tests := []struct {
		name     string
		filename string
		version  int
		expected string
	}{
		{
			name:     "regular file",
			filename: "test.txt",
			version:  1,
			expected: "testapp/user123/session456/test.txt/1",
		},
		{
			name:     "user namespaced file",
			filename: "user:document.pdf",
			version:  5,
			expected: "testapp/user123/user/user:document.pdf/5",
		},
		{
			name:     "version 0",
			filename: "test.txt",
			version:  0,
			expected: "testapp/user123/session456/test.txt/0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildObjectName(sessionInfo, tt.filename, tt.version)
			if result != tt.expected {
				t.Errorf("BuildObjectName() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestBuildObjectNamePrefix(t *testing.T) {
	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	tests := []struct {
		name     string
		filename string
		expected string
	}{
		{
			name:     "regular file",
			filename: "test.txt",
			expected: "testapp/user123/session456/test.txt/",
		},
		{
			name:     "user namespaced file",
			filename: "user:document.pdf",
			expected: "testapp/user123/user/user:document.pdf/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildObjectNamePrefix(sessionInfo, tt.filename)
			if result != tt.expected {
				t.Errorf("BuildObjectNamePrefix() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestBuildSessionPrefix(t *testing.T) {
	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	expected := "testapp/user123/session456/"
	result := BuildSessionPrefix(sessionInfo)
	if result != expected {
		t.Errorf("BuildSessionPrefix() = %v, want %v", result, expected)
	}
}

func TestBuildUserNamespacePrefix(t *testing.T) {
	sessionInfo := artifact.SessionInfo{
		AppName: "testapp",
		UserID:  "user123",
	}

	expected := "testapp/user123/user/"
	result := BuildUserNamespacePrefix(sessionInfo)
	if result != expected {
		t.Errorf("BuildUserNamespacePrefix() = %v, want %v", result, expected)
	}
}
