//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestStageInputsArtifactDefaultNameAndPin(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewService()
	info := artifact.SessionInfo{
		AppName:   "sandbox-artifact-test",
		UserID:    "user",
		SessionID: "pin",
	}
	ctx = codeexecutor.WithArtifactService(ctx, svc)
	ctx = codeexecutor.WithArtifactSession(ctx, info)

	ver0, err := codeexecutor.SaveArtifactHelper(
		ctx, "uploads/numbers.txt", []byte("first"), "text/plain",
	)
	if err != nil {
		t.Fatal(err)
	}
	if ver0 != 0 {
		t.Fatalf("first artifact version = %d, want 0", ver0)
	}

	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile()),
	)
	ws, err := rt.CreateWorkspace(ctx, "artifact/pin", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "artifact://uploads/numbers.txt@0",
	}}); err != nil {
		t.Fatal(err)
	}
	files, err := rt.Collect(ctx, ws, []string{"work/inputs/numbers.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Content != "first" {
		t.Fatalf("staged artifact = %#v, want first", files)
	}

	if _, err := codeexecutor.SaveArtifactHelper(
		ctx, "uploads/numbers.txt", []byte("second"), "text/plain",
	); err != nil {
		t.Fatal(err)
	}
	if err := rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "artifact://uploads/numbers.txt",
		To:   "work/pinned.txt",
		Pin:  true,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := codeexecutor.SaveArtifactHelper(
		ctx, "uploads/numbers.txt", []byte("third"), "text/plain",
	); err != nil {
		t.Fatal(err)
	}
	if err := rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "artifact://uploads/numbers.txt",
		To:   "work/pinned.txt",
		Pin:  true,
	}}); err != nil {
		t.Fatal(err)
	}
	files, err = rt.Collect(ctx, ws, []string{"work/pinned.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Content != "second" {
		t.Fatalf("pinned staged artifact = %#v, want second", files)
	}
}

func TestStageInputsArtifactErrors(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile()),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "artifact/errors", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	err = rt.StageInputs(context.Background(), ws, []codeexecutor.InputSpec{{
		From: "artifact://demo.txt",
		To:   "work/demo.txt",
	}})
	if err == nil || !strings.Contains(err.Error(), "artifact service") {
		t.Fatalf("expected missing artifact service error, got %v", err)
	}
	err = rt.StageInputs(context.Background(), ws, []codeexecutor.InputSpec{{
		From: "artifact://@0",
		To:   "work/bad.txt",
	}})
	if err == nil || !strings.Contains(err.Error(), "invalid artifact ref") {
		t.Fatalf("expected invalid artifact ref error, got %v", err)
	}
}

func TestCollectOutputsSaveInlineArtifact(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewService()
	info := artifact.SessionInfo{
		AppName:   "sandbox-artifact-test",
		UserID:    "user",
		SessionID: "collect",
	}
	ctx = codeexecutor.WithArtifactService(ctx, svc)
	ctx = codeexecutor.WithArtifactSession(ctx, info)

	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile()),
	)
	ws, err := rt.CreateWorkspace(ctx, "artifact/collect", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "out/report.txt",
		Content: []byte("artifact report"),
	}}); err != nil {
		t.Fatal(err)
	}
	manifest, err := rt.CollectOutputs(ctx, ws, codeexecutor.OutputSpec{
		Globs:        []string{"out/report.txt"},
		Save:         true,
		Inline:       true,
		NameTemplate: "saved/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 1 {
		t.Fatalf("manifest files = %#v, want 1", manifest.Files)
	}
	got := manifest.Files[0]
	if got.Content != "artifact report" {
		t.Fatalf("inline content = %q, want artifact report", got.Content)
	}
	if got.SavedAs != "saved/out/report.txt" || got.Version != 0 {
		t.Fatalf("saved ref = %s@%d, want saved/out/report.txt@0", got.SavedAs, got.Version)
	}
	data, _, actual, err := codeexecutor.LoadArtifactHelper(
		ctx, "saved/out/report.txt", &got.Version,
	)
	if err != nil {
		t.Fatal(err)
	}
	if actual != 0 || string(data) != "artifact report" {
		t.Fatalf("loaded artifact actual=%d data=%q", actual, string(data))
	}
	md, err := codeexecutor.LoadMetadata(ws.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(md.Outputs) != 1 ||
		len(md.Outputs[0].SavedAs) != 1 ||
		md.Outputs[0].SavedAs[0] != "saved/out/report.txt" {
		t.Fatalf("metadata outputs = %#v", md.Outputs)
	}
}

func TestCollectOutputsSaveTruncatedFileErrors(t *testing.T) {
	ctx := context.Background()
	svc := inmemory.NewService()
	ctx = codeexecutor.WithArtifactService(ctx, svc)
	ctx = codeexecutor.WithArtifactSession(ctx, artifact.SessionInfo{
		AppName:   "sandbox-artifact-test",
		UserID:    "user",
		SessionID: "truncated",
	})

	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile()),
	)
	ws, err := rt.CreateWorkspace(ctx, "artifact/truncated", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "out/large.txt",
		Content: []byte("0123456789"),
	}}); err != nil {
		t.Fatal(err)
	}
	_, err = rt.CollectOutputs(ctx, ws, codeexecutor.OutputSpec{
		Globs:        []string{"out/large.txt"},
		Save:         true,
		MaxFileBytes: 4,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot save truncated output file") {
		t.Fatalf("expected truncated save error, got %v", err)
	}
}

func TestCollectOutputsMetadataLoadError(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile()),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "artifact/bad-metadata", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(ws.Path, codeexecutor.MetaFileName),
		[]byte("{bad json"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(ws.Path, codeexecutor.DirOut, "report.txt"),
		[]byte("ok"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	_, err = rt.CollectOutputs(context.Background(), ws, codeexecutor.OutputSpec{
		Globs: []string{"out/report.txt"},
	})
	if err == nil {
		t.Fatalf("expected metadata load error")
	}
}
