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
