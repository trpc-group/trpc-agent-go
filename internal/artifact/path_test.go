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

func TestBuildArtifactPath(t *testing.T) {
	tests := []struct {
		name     string
		key      artifact.Key
		expected string
	}{
		{
			name:     "regular file",
			key:      artifact.Key{AppName: "testapp", UserID: "user123", SessionID: "session456", Name: "test.txt"},
			expected: "testapp/user123/session456/test.txt",
		},
		{
			name:     "user-scoped file",
			key:      artifact.Key{AppName: "testapp", UserID: "user123", Name: "document.pdf"},
			expected: "testapp/user123/user/document.pdf",
		},
		{
			name:     "empty name",
			key:      artifact.Key{AppName: "testapp", UserID: "user123", SessionID: "session456", Name: ""},
			expected: "testapp/user123/session456/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildArtifactPath(tt.key)
			if result != tt.expected {
				t.Errorf("BuildArtifactPath() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestBuildObjectName(t *testing.T) {
	tests := []struct {
		name     string
		key      artifact.Key
		version  artifact.VersionID
		expected string
	}{
		{
			name:     "regular file",
			key:      artifact.Key{AppName: "testapp", UserID: "user123", SessionID: "session456", Name: "test.txt"},
			version:  "1",
			expected: "testapp/user123/session456/test.txt/1",
		},
		{
			name:     "user-scoped file",
			key:      artifact.Key{AppName: "testapp", UserID: "user123", Name: "document.pdf"},
			version:  "5",
			expected: "testapp/user123/user/document.pdf/5",
		},
		{
			name:     "version 0",
			key:      artifact.Key{AppName: "testapp", UserID: "user123", SessionID: "session456", Name: "test.txt"},
			version:  "0",
			expected: "testapp/user123/session456/test.txt/0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildObjectName(tt.key, tt.version)
			if result != tt.expected {
				t.Errorf("BuildObjectName() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestBuildObjectNamePrefix(t *testing.T) {
	tests := []struct {
		name     string
		key      artifact.Key
		expected string
	}{
		{
			name:     "regular file",
			key:      artifact.Key{AppName: "testapp", UserID: "user123", SessionID: "session456", Name: "test.txt"},
			expected: "testapp/user123/session456/test.txt/",
		},
		{
			name:     "user-scoped file",
			key:      artifact.Key{AppName: "testapp", UserID: "user123", Name: "document.pdf"},
			expected: "testapp/user123/user/document.pdf/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildObjectNamePrefix(tt.key)
			if result != tt.expected {
				t.Errorf("BuildObjectNamePrefix() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestBuildSessionPrefix(t *testing.T) {
	expected := "testapp/user123/session456/"
	result := BuildSessionPrefix("testapp", "user123", "session456")
	if result != expected {
		t.Errorf("BuildSessionPrefix() = %v, want %v", result, expected)
	}
}

func TestBuildUserNamespacePrefix(t *testing.T) {
	expected := "testapp/user123/user/"
	result := BuildUserNamespacePrefix("testapp", "user123")
	if result != expected {
		t.Errorf("BuildUserNamespacePrefix() = %v, want %v", result, expected)
	}
}
