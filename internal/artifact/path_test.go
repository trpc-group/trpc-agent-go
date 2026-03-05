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
		name      string
		appName   string
		userID    string
		sessionID string
		fileName  string
		expected  string
	}{
		{
			name:      "regular file",
			appName:   "testapp",
			userID:    "user123",
			sessionID: "session456",
			fileName:  "test.txt",
			expected:  "testapp/user123/session456/test.txt",
		},
		{
			name:     "user-scoped file",
			appName:  "testapp",
			userID:   "user123",
			fileName: "document.pdf",
			expected: "testapp/user123/user/document.pdf",
		},
		{
			name:      "empty name",
			appName:   "testapp",
			userID:    "user123",
			sessionID: "session456",
			fileName:  "",
			expected:  "testapp/user123/session456/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildArtifactPath(tt.appName, tt.userID, tt.sessionID, tt.fileName)
			if result != tt.expected {
				t.Errorf("BuildArtifactPath() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestBuildObjectName(t *testing.T) {
	tests := []struct {
		name      string
		appName   string
		userID    string
		sessionID string
		fileName  string
		version   artifact.VersionID
		expected  string
	}{
		{
			name:      "regular file",
			appName:   "testapp",
			userID:    "user123",
			sessionID: "session456",
			fileName:  "test.txt",
			version:   "1",
			expected:  "testapp/user123/session456/test.txt/1",
		},
		{
			name:     "user-scoped file",
			appName:  "testapp",
			userID:   "user123",
			fileName: "document.pdf",
			version:  "5",
			expected: "testapp/user123/user/document.pdf/5",
		},
		{
			name:      "version 0",
			appName:   "testapp",
			userID:    "user123",
			sessionID: "session456",
			fileName:  "test.txt",
			version:   "0",
			expected:  "testapp/user123/session456/test.txt/0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildObjectName(tt.appName, tt.userID, tt.sessionID, tt.fileName, tt.version)
			if result != tt.expected {
				t.Errorf("BuildObjectName() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestBuildObjectNamePrefix(t *testing.T) {
	tests := []struct {
		name      string
		appName   string
		userID    string
		sessionID string
		fileName  string
		expected  string
	}{
		{
			name:      "regular file",
			appName:   "testapp",
			userID:    "user123",
			sessionID: "session456",
			fileName:  "test.txt",
			expected:  "testapp/user123/session456/test.txt/",
		},
		{
			name:     "user-scoped file",
			appName:  "testapp",
			userID:   "user123",
			fileName: "document.pdf",
			expected: "testapp/user123/user/document.pdf/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildObjectNamePrefix(tt.appName, tt.userID, tt.sessionID, tt.fileName)
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
