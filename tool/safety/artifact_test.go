//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestRedactingArtifactServiceProtectsStoredData(t *testing.T) {
	const (
		secret         = "ordinary-access-token-value"
		passwordSecret = "database password value"
	)

	ctx := context.Background()
	info := artifact.SessionInfo{
		AppName:   "safety-test",
		UserID:    "user",
		SessionID: "session",
	}
	base := inmemory.NewService()
	service := NewRedactingArtifactService(base)

	original := &artifact.Artifact{
		Data: []byte(
			`{"access_token":"` + secret +
				`","db_password":"` + passwordSecret +
				`","safe":"ok"}`,
		),
		MimeType: "application/json",
		Name:     "result.json",
	}
	if _, err := service.SaveArtifact(
		ctx,
		info,
		"result.json",
		original,
	); err != nil {
		t.Fatalf("SaveArtifact(text) error = %v", err)
	}

	stored, err := base.LoadArtifact(
		ctx,
		info,
		"result.json",
		nil,
	)
	if err != nil {
		t.Fatalf("LoadArtifact(text) error = %v", err)
	}
	if stored == nil {
		t.Fatal("LoadArtifact(text) = nil")
	}
	if bytes.Contains(stored.Data, []byte(secret)) {
		t.Errorf("stored text artifact contains secret: %s", stored.Data)
	}
	if bytes.Contains(stored.Data, []byte(passwordSecret)) {
		t.Errorf(
			"stored text artifact contains field-name secret: %s",
			stored.Data,
		)
	}
	if !bytes.Contains(stored.Data, []byte("[REDACTED]")) {
		t.Errorf("stored text artifact has no redaction marker: %s", stored.Data)
	}
	if !bytes.Contains(stored.Data, []byte(`"safe":"ok"`)) {
		t.Errorf("safe text was removed from artifact: %s", stored.Data)
	}
	if !bytes.Contains(original.Data, []byte(secret)) {
		t.Errorf("SaveArtifact() mutated original artifact: %s", original.Data)
	}

	binary := &artifact.Artifact{
		Data: append(
			[]byte{0x00, 0x01, 0x02},
			[]byte("password="+secret)...,
		),
		MimeType: "application/octet-stream",
		Name:     "result.bin",
	}
	_, err = service.SaveArtifact(
		ctx,
		info,
		"result.bin",
		binary,
	)
	if err == nil {
		t.Fatal("SaveArtifact(secret binary) succeeded")
	}
	if !strings.Contains(
		err.Error(),
		"artifact contains a recognized secret",
	) {
		t.Errorf(
			"SaveArtifact(secret binary) error = %q",
			err,
		)
	}
	stored, err = base.LoadArtifact(
		ctx,
		info,
		"result.bin",
		nil,
	)
	if err != nil {
		t.Fatalf("LoadArtifact(binary) error = %v", err)
	}
	if stored != nil {
		t.Errorf("secret binary artifact was stored: %+v", stored)
	}
}

func TestRedactingArtifactServiceProtectsCodeExecutorSavePath(
	t *testing.T,
) {
	const secret = "correct-horse-battery-staple"

	info := artifact.SessionInfo{
		AppName:   "safety-test",
		UserID:    "user",
		SessionID: "codeexecutor",
	}
	base := inmemory.NewService()
	service := NewRedactingArtifactService(base)
	ctx := codeexecutor.WithArtifactService(
		context.Background(),
		service,
	)
	ctx = codeexecutor.WithArtifactSession(ctx, info)

	if _, err := codeexecutor.SaveArtifactHelper(
		ctx,
		"result.txt",
		[]byte("password="+secret+"\nsafe=ok"),
		"text/plain",
	); err != nil {
		t.Fatalf("SaveArtifactHelper(text) error = %v", err)
	}
	stored, err := base.LoadArtifact(
		ctx,
		info,
		"result.txt",
		nil,
	)
	if err != nil {
		t.Fatalf("LoadArtifact(text) error = %v", err)
	}
	if stored == nil {
		t.Fatal("LoadArtifact(text) = nil")
	}
	if bytes.Contains(stored.Data, []byte(secret)) {
		t.Errorf(
			"codeexecutor artifact contains secret: %s",
			stored.Data,
		)
	}
	if !bytes.Contains(stored.Data, []byte("safe=ok")) {
		t.Errorf(
			"safe codeexecutor artifact data was removed: %s",
			stored.Data,
		)
	}

	safeBinary := []byte{0x00, 0x01, 0x02, 0xff}
	if _, err := codeexecutor.SaveArtifactHelper(
		ctx,
		"safe.bin",
		safeBinary,
		"application/octet-stream",
	); err != nil {
		t.Fatalf("SaveArtifactHelper(safe binary) error = %v", err)
	}
	stored, err = base.LoadArtifact(
		ctx,
		info,
		"safe.bin",
		nil,
	)
	if err != nil {
		t.Fatalf("LoadArtifact(safe binary) error = %v", err)
	}
	if stored == nil || !bytes.Equal(stored.Data, safeBinary) {
		t.Errorf(
			"stored safe binary = %+v, want %v",
			stored,
			safeBinary,
		)
	}

	if _, err := codeexecutor.SaveArtifactHelper(
		ctx,
		"invalid.json",
		[]byte(`{"password":`),
		"application/json",
	); err == nil {
		t.Fatal("SaveArtifactHelper(invalid JSON) succeeded")
	}
	stored, err = base.LoadArtifact(
		ctx,
		info,
		"invalid.json",
		nil,
	)
	if err != nil {
		t.Fatalf("LoadArtifact(invalid JSON) error = %v", err)
	}
	if stored != nil {
		t.Errorf("invalid JSON artifact was stored: %+v", stored)
	}
}

func TestRedactingArtifactServiceDelegatesReadAndDeleteOperations(
	t *testing.T,
) {
	ctx := context.Background()
	info := artifact.SessionInfo{
		AppName:   "safety-test",
		UserID:    "user",
		SessionID: "delegation",
	}
	base := inmemory.NewService()
	service := NewRedactingArtifactService(base)

	for _, data := range []string{"first", "second"} {
		if _, err := service.SaveArtifact(
			ctx,
			info,
			"result.txt",
			&artifact.Artifact{
				Data:     []byte(data),
				MimeType: "text/plain",
			},
		); err != nil {
			t.Fatalf("SaveArtifact() error = %v", err)
		}
	}

	loaded, err := service.LoadArtifact(ctx, info, "result.txt", nil)
	if err != nil {
		t.Fatalf("LoadArtifact() error = %v", err)
	}
	if loaded == nil || string(loaded.Data) != "second" {
		t.Fatalf("LoadArtifact() = %+v, want latest artifact", loaded)
	}

	keys, err := service.ListArtifactKeys(ctx, info)
	if err != nil {
		t.Fatalf("ListArtifactKeys() error = %v", err)
	}
	if len(keys) != 1 || keys[0] != "result.txt" {
		t.Fatalf("ListArtifactKeys() = %v, want [result.txt]", keys)
	}

	versions, err := service.ListVersions(ctx, info, "result.txt")
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if len(versions) != 2 || versions[0] != 0 || versions[1] != 1 {
		t.Fatalf("ListVersions() = %v, want [0 1]", versions)
	}

	if err := service.DeleteArtifact(ctx, info, "result.txt"); err != nil {
		t.Fatalf("DeleteArtifact() error = %v", err)
	}
	loaded, err = service.LoadArtifact(ctx, info, "result.txt", nil)
	if err != nil {
		t.Fatalf("LoadArtifact(deleted) error = %v", err)
	}
	if loaded != nil {
		t.Fatalf("LoadArtifact(deleted) = %+v, want nil", loaded)
	}
}

func TestRedactingArtifactServiceRequiresDelegate(t *testing.T) {
	ctx := context.Background()
	service := &RedactingArtifactService{}
	info := artifact.SessionInfo{}
	assertDelegateError := func(name string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "delegate is required") {
			t.Errorf("%s error = %v, want missing delegate", name, err)
		}
	}

	_, err := service.SaveArtifact(ctx, info, "result.txt", nil)
	assertDelegateError("SaveArtifact", err)
	_, err = service.LoadArtifact(ctx, info, "result.txt", nil)
	assertDelegateError("LoadArtifact", err)
	_, err = service.ListArtifactKeys(ctx, info)
	assertDelegateError("ListArtifactKeys", err)
	assertDelegateError(
		"DeleteArtifact",
		service.DeleteArtifact(ctx, info, "result.txt"),
	)
	_, err = service.ListVersions(ctx, info, "result.txt")
	assertDelegateError("ListVersions", err)
}
