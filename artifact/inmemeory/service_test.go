//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

func TestNewService(t *testing.T) {
	service := NewService()

	assert.NotNil(t, service)
	assert.NotNil(t, service.artifacts)
	assert.Equal(t, 0, len(service.artifacts))
}

func TestSaveArtifact(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	testArtifact := &artifact.Artifact{
		Data:     []byte("test data"),
		MimeType: "text/plain",
		Name:     "test.txt",
	}

	// Save first version
	version, err := service.SaveArtifact(ctx, sessionInfo, "test.txt", testArtifact)
	require.NoError(t, err)
	assert.Equal(t, 0, version)

	// Save second version
	testArtifact2 := &artifact.Artifact{
		Data:     []byte("test data v2"),
		MimeType: "text/plain",
		Name:     "test.txt",
	}

	version, err = service.SaveArtifact(ctx, sessionInfo, "test.txt", testArtifact2)
	require.NoError(t, err)
	assert.Equal(t, 1, version)
}

func TestLoadArtifact(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	// Test loading non-existent artifact
	loadedArtifact, err := service.LoadArtifact(ctx, sessionInfo, "nonexistent.txt", nil)
	require.NoError(t, err)
	assert.Nil(t, loadedArtifact)

	// Save an artifact first
	testArtifact := &artifact.Artifact{
		Data:     []byte("test data"),
		MimeType: "text/plain",
		Name:     "test.txt",
	}

	_, err = service.SaveArtifact(ctx, sessionInfo, "test.txt", testArtifact)
	require.NoError(t, err)

	// Load latest version (nil version)
	loadedArtifact, err = service.LoadArtifact(ctx, sessionInfo, "test.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, testArtifact, loadedArtifact)

	// Load specific version
	version := 0
	loadedArtifact, err = service.LoadArtifact(ctx, sessionInfo, "test.txt", &version)
	require.NoError(t, err)
	assert.Equal(t, testArtifact, loadedArtifact)

	// Test loading invalid version
	invalidVersion := 999
	_, err = service.LoadArtifact(ctx, sessionInfo, "test.txt", &invalidVersion)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "version 999 does not exist")
}

func TestLoadArtifactMultipleVersions(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	// Save multiple versions
	artifacts := []*artifact.Artifact{
		{Data: []byte("version 0"), MimeType: "text/plain", Name: "test.txt"},
		{Data: []byte("version 1"), MimeType: "text/plain", Name: "test.txt"},
		{Data: []byte("version 2"), MimeType: "text/plain", Name: "test.txt"},
	}

	for i, art := range artifacts {
		version, err := service.SaveArtifact(ctx, sessionInfo, "test.txt", art)
		require.NoError(t, err)
		assert.Equal(t, i, version)
	}

	// Load latest version (should be version 2)
	latestArtifact, err := service.LoadArtifact(ctx, sessionInfo, "test.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, artifacts[2], latestArtifact)

	// Load specific versions
	for i, expectedArt := range artifacts {
		version := i
		loadedArtifact, err := service.LoadArtifact(ctx, sessionInfo, "test.txt", &version)
		require.NoError(t, err)
		assert.Equal(t, expectedArt, loadedArtifact)
	}
}

func TestListArtifactKeys(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	// Test empty list
	keys, err := service.ListArtifactKeys(ctx, sessionInfo)
	require.NoError(t, err)
	assert.Empty(t, keys)

	// Add session-scoped artifacts
	testArtifact := &artifact.Artifact{
		Data:     []byte("test data"),
		MimeType: "text/plain",
		Name:     "test.txt",
	}

	_, err = service.SaveArtifact(ctx, sessionInfo, "document.pdf", testArtifact)
	require.NoError(t, err)

	_, err = service.SaveArtifact(ctx, sessionInfo, "image.png", testArtifact)
	require.NoError(t, err)

	// Add user-namespaced artifact
	_, err = service.SaveArtifact(ctx, sessionInfo, "user:profile.jpg", testArtifact)
	require.NoError(t, err)

	// List keys
	keys, err = service.ListArtifactKeys(ctx, sessionInfo)
	require.NoError(t, err)

	expected := []string{"document.pdf", "image.png", "user:profile.jpg"}
	assert.ElementsMatch(t, expected, keys)

	// Keys should be sorted
	assert.Equal(t, []string{"document.pdf", "image.png", "user:profile.jpg"}, keys)
}

func TestListArtifactKeys_DifferentSessions(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	sessionInfo1 := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session1",
	}

	sessionInfo2 := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session2",
	}

	testArtifact := &artifact.Artifact{
		Data:     []byte("test data"),
		MimeType: "text/plain",
		Name:     "test.txt",
	}

	// Add artifacts to different sessions
	_, err := service.SaveArtifact(ctx, sessionInfo1, "file1.txt", testArtifact)
	require.NoError(t, err)

	_, err = service.SaveArtifact(ctx, sessionInfo2, "file2.txt", testArtifact)
	require.NoError(t, err)

	// Add user-namespaced artifact (should appear in both sessions)
	_, err = service.SaveArtifact(ctx, sessionInfo1, "user:shared.txt", testArtifact)
	require.NoError(t, err)

	// List keys for session1
	keys1, err := service.ListArtifactKeys(ctx, sessionInfo1)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"file1.txt", "user:shared.txt"}, keys1)

	// List keys for session2
	keys2, err := service.ListArtifactKeys(ctx, sessionInfo2)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"file2.txt", "user:shared.txt"}, keys2)
}

func TestDeleteArtifact(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	// Delete non-existent artifact (should not error)
	err := service.DeleteArtifact(ctx, sessionInfo, "nonexistent.txt")
	assert.NoError(t, err)

	// Save an artifact
	testArtifact := &artifact.Artifact{
		Data:     []byte("test data"),
		MimeType: "text/plain",
		Name:     "test.txt",
	}

	_, err = service.SaveArtifact(ctx, sessionInfo, "test.txt", testArtifact)
	require.NoError(t, err)

	// Verify artifact exists
	keys, err := service.ListArtifactKeys(ctx, sessionInfo)
	require.NoError(t, err)
	assert.Contains(t, keys, "test.txt")

	// Delete the artifact
	err = service.DeleteArtifact(ctx, sessionInfo, "test.txt")
	assert.NoError(t, err)

	// Verify artifact is deleted
	keys, err = service.ListArtifactKeys(ctx, sessionInfo)
	require.NoError(t, err)
	assert.NotContains(t, keys, "test.txt")

	// Try to load deleted artifact
	loadedArtifact, err := service.LoadArtifact(ctx, sessionInfo, "test.txt", nil)
	require.NoError(t, err)
	assert.Nil(t, loadedArtifact)
}

func TestListVersions(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	// Test listing versions for non-existent artifact
	versions, err := service.ListVersions(ctx, sessionInfo, "nonexistent.txt")
	require.NoError(t, err)
	assert.Empty(t, versions)

	// Save multiple versions
	testArtifact := &artifact.Artifact{
		Data:     []byte("test data"),
		MimeType: "text/plain",
		Name:     "test.txt",
	}

	for i := 0; i < 3; i++ {
		_, err := service.SaveArtifact(ctx, sessionInfo, "test.txt", testArtifact)
		require.NoError(t, err)
	}

	// List versions
	versions, err = service.ListVersions(ctx, sessionInfo, "test.txt")
	require.NoError(t, err)
	assert.Equal(t, []int{0, 1, 2}, versions)
}

func TestUserNamespacedArtifacts(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	sessionInfo1 := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session1",
	}

	sessionInfo2 := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session2",
	}

	testArtifact := &artifact.Artifact{
		Data:     []byte("user data"),
		MimeType: "text/plain",
		Name:     "user:profile.txt",
	}

	// Save user-namespaced artifact in session1
	version, err := service.SaveArtifact(ctx, sessionInfo1, "user:profile.txt", testArtifact)
	require.NoError(t, err)
	assert.Equal(t, 0, version)

	// User-namespaced artifact should be accessible from both sessions
	loadedFromSession1, err := service.LoadArtifact(ctx, sessionInfo1, "user:profile.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, testArtifact, loadedFromSession1)

	loadedFromSession2, err := service.LoadArtifact(ctx, sessionInfo2, "user:profile.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, testArtifact, loadedFromSession2)

	// User-namespaced artifact should appear in both session's key lists
	keys1, err := service.ListArtifactKeys(ctx, sessionInfo1)
	require.NoError(t, err)
	assert.Contains(t, keys1, "user:profile.txt")

	keys2, err := service.ListArtifactKeys(ctx, sessionInfo2)
	require.NoError(t, err)
	assert.Contains(t, keys2, "user:profile.txt")
}

func TestConcurrentAccess(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	testArtifact := &artifact.Artifact{
		Data:     []byte("test data"),
		MimeType: "text/plain",
		Name:     "test.txt",
	}

	// Test concurrent writes
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(index int) {
			filename := fmt.Sprintf("file%d.txt", index)
			_, err := service.SaveArtifact(ctx, sessionInfo, filename, testArtifact)
			assert.NoError(t, err)
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all files were saved
	keys, err := service.ListArtifactKeys(ctx, sessionInfo)
	require.NoError(t, err)
	assert.Len(t, keys, 10)

	// Test concurrent reads
	for i := 0; i < 10; i++ {
		go func(index int) {
			filename := fmt.Sprintf("file%d.txt", index)
			loadedArtifact, err := service.LoadArtifact(ctx, sessionInfo, filename, nil)
			assert.NoError(t, err)
			assert.NotNil(t, loadedArtifact)
			done <- true
		}(i)
	}

	// Wait for all read goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestArtifactPathGeneration(t *testing.T) {
	service := NewService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	testArtifact := &artifact.Artifact{
		Data:     []byte("test data"),
		MimeType: "text/plain",
		Name:     "test.txt",
	}

	// Save regular artifact
	_, err := service.SaveArtifact(ctx, sessionInfo, "regular.txt", testArtifact)
	require.NoError(t, err)

	// Save user-namespaced artifact
	_, err = service.SaveArtifact(ctx, sessionInfo, "user:namespaced.txt", testArtifact)
	require.NoError(t, err)

	// Check that artifacts are stored with correct paths
	assert.Len(t, service.artifacts, 2)

	// Verify path structure by checking if artifacts can be loaded
	regularArtifact, err := service.LoadArtifact(ctx, sessionInfo, "regular.txt", nil)
	require.NoError(t, err)
	assert.NotNil(t, regularArtifact)

	userArtifact, err := service.LoadArtifact(ctx, sessionInfo, "user:namespaced.txt", nil)
	require.NoError(t, err)
	assert.NotNil(t, userArtifact)
}
