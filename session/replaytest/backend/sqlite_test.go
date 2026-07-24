//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package backend

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestNewSQLiteService(t *testing.T) {
	svc := NewSQLiteService(t)
	ctx := context.Background()
	key := session.Key{AppName: "replaytest", UserID: "u1", SessionID: "s1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	if err != nil || sess == nil {
		t.Fatalf("创建 session 失败: %v", err)
	}
	got, err := svc.GetSession(ctx, key)
	if err != nil || got.ID != sess.ID {
		t.Fatalf("读取 session 失败: %v, %+v", err, got)
	}
}
