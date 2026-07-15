// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package reviewer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/fakemodel"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/persistence"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewinput"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/workspaceexec"

	_ "modernc.org/sqlite"
)

func TestNewReviewModelDefaultsToConfiguredRealModel(t *testing.T) {
	r := &reviewer{config: Config{Model: ModelConfig{
		Name:    "real-model",
		APIKey:  "test-key",
		BaseURL: "https://example.invalid",
	}}}
	got, err := r.newReviewModel("")
	if err != nil {
		t.Fatal(err)
	}
	if got.Info().Name != "real-model" {
		t.Fatalf("model name = %q, want real-model", got.Info().Name)
	}
}

func TestNewReviewModelSelectsFixtureFakeModel(t *testing.T) {
	r := &reviewer{config: Config{Mode: "fake-model"}}
	got, err := r.newReviewModel("acceptance-security")
	if err != nil {
		t.Fatal(err)
	}
	fake, ok := got.(*fakemodel.FakeModel)
	if !ok {
		t.Fatalf("model type = %T, want *fakemodel.FakeModel", got)
	}
	if fake.Fixture() != "acceptance-security" {
		t.Fatalf("fixture = %q, want acceptance-security", fake.Fixture())
	}
}

func TestNewReviewModelRequiresFixtureForFakeModel(t *testing.T) {
	r := &reviewer{config: Config{Mode: "fake-model"}}
	_, err := r.newReviewModel("")
	if err == nil || !strings.Contains(err.Error(), "fixture") {
		t.Fatalf("newReviewModel error = %v, want fixture requirement", err)
	}
}

func TestNewReviewModelUsesConfiguredRealModelForOtherModes(t *testing.T) {
	for _, mode := range []string{"fake", "real"} {
		t.Run(mode, func(t *testing.T) {
			r := &reviewer{config: Config{
				Mode: mode,
				Model: ModelConfig{
					Name:    "real-model",
					APIKey:  "test-key",
					BaseURL: "https://example.invalid",
				},
			}}
			got, err := r.newReviewModel("acceptance-clean")
			if err != nil {
				t.Fatal(err)
			}
			if got.Info().Name != "real-model" {
				t.Fatalf("model name = %q, want real-model", got.Info().Name)
			}
		})
	}
}

func TestReviewRecordsInputPreparationFailureOnTask(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	sanitizer := redact.New()
	resources, err := persistence.Open(ctx, dbPath, redact.AppendEventHook(sanitizer))
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := NewReviewer(Dependencies{
		Store:           resources.ReviewStore,
		SessionService:  resources.SessionService,
		ArtifactService: resources.ArtifactService,
		Sanitizer:       sanitizer,
	}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	reviewErr := reviewer.Review(ctx, reviewinput.Spec{
		DiffFile: filepath.Join(t.TempDir(), "password=reviewer-secret-value"),
	})
	if reviewErr == nil || !strings.Contains(reviewErr.Error(), "read diff file") {
		t.Fatalf("Review error = %v, want missing diff error", reviewErr)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status, inputKind, errorMessage string
	if err := db.QueryRow(`
SELECT task_status, input_kind, error_message
FROM review_tasks ORDER BY created_at DESC LIMIT 1`).Scan(&status, &inputKind, &errorMessage); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || inputKind != reviewinput.InputKindDiffFile || !strings.Contains(errorMessage, "read diff file") {
		t.Fatalf("task = status:%s kind:%s error:%s", status, inputKind, errorMessage)
	}
	if strings.Contains(errorMessage, "reviewer-secret-value") {
		t.Fatalf("stored task error contains plaintext: %s", errorMessage)
	}
}

func TestRedactingToolCallbackReplacesCredentialBearingResult(t *testing.T) {
	callbacks := newRedactingToolCallbacks(redact.New())
	result, err := callbacks.RunAfterTool(context.Background(), &tool.AfterToolArgs{
		ToolName: "workspace_exec",
		Result: map[string]any{
			"status": "completed",
			"output": "password=tool-output-secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.CustomResult == nil {
		t.Fatal("credential-bearing result was not replaced")
	}
	if strings.Contains(toString(result.CustomResult), "tool-output-secret") {
		t.Fatalf("custom result contains plaintext: %#v", result.CustomResult)
	}
}

func TestRedactingToolCallbackReplacesCredentialBearingError(t *testing.T) {
	callbacks := newRedactingToolCallbacks(redact.New())
	result, err := callbacks.RunAfterTool(context.Background(), &tool.AfterToolArgs{
		ToolName: "workspace_exec",
		Error:    fmt.Errorf("command failed with token=tool-error-secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.CustomResult == nil {
		t.Fatal("credential-bearing error was not replaced")
	}
	if strings.Contains(toString(result.CustomResult), "tool-error-secret") {
		t.Fatalf("custom error result contains plaintext: %#v", result.CustomResult)
	}
}

func TestWorkspaceArtifactContextStagesReviewInput(t *testing.T) {
	ctx := context.Background()
	service := inmemory.NewService()
	info := artifact.SessionInfo{
		AppName:   codeReviewAgentName,
		UserID:    "reviewer",
		SessionID: "review-task",
	}
	const artifactName = "review_input.diff"
	version, err := service.SaveArtifact(ctx, info, artifactName, &artifact.Artifact{
		Data:     []byte("masked review input"),
		MimeType: "text/x-diff",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx = withWorkspaceArtifactContext(ctx, service, info)
	executor := localexec.New(localexec.WithWorkDir(t.TempDir()))
	execTool := workspaceexec.NewExecTool(
		executor,
		workspaceexec.WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{
			Files: []codeexecutor.WorkspaceFile{{
				Key:    "review-input-diff",
				Target: "work/inputs/change.diff",
				Input: &codeexecutor.InputSpec{
					From: fmt.Sprintf("artifact://%s@%d", artifactName, version),
					Mode: "copy",
					Pin:  true,
				},
			}},
		}),
	)
	result, err := execTool.Call(ctx, []byte(`{"command":"cat work/inputs/change.diff"}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "masked review input") {
		t.Fatalf("workspace_exec result = %s", encoded)
	}
}

func toString(value any) string {
	return fmt.Sprint(value)
}
