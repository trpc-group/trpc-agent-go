//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRuntimeState(t *testing.T) {
	t.Parallel()

	require.Nil(t, RuntimeState(" "))
	require.Equal(
		t,
		map[string]any{runtimeStateKeyUserID: "u-1"},
		RuntimeState(" u-1 "),
	)
}

func TestResolveUserID(t *testing.T) {
	t.Parallel()

	sess := session.NewSession("app", "session-user", "sess-1")

	userID, ok := ResolveUserID(sess, nil)
	require.True(t, ok)
	require.Equal(t, "session-user", userID)

	sess.SetState(sessionStateKeyUserID, []byte("state-user"))
	userID, ok = ResolveUserID(sess, nil)
	require.True(t, ok)
	require.Equal(t, "state-user", userID)

	userID, ok = ResolveUserID(
		sess,
		RuntimeState("runtime-user"),
	)
	require.True(t, ok)
	require.Equal(t, "runtime-user", userID)

	userID, ok = ResolveUserID(nil, RuntimeState("runtime-user"))
	require.True(t, ok)
	require.Equal(t, "runtime-user", userID)

	userID, ok = ResolveUserID(
		session.NewSession("app", " ", "sess-1"),
		map[string]any{runtimeStateKeyUserID: 123},
	)
	require.False(t, ok)
	require.Empty(t, userID)

	userID, ok = ResolveUserID(nil, nil)
	require.False(t, ok)
	require.Empty(t, userID)

	blankStateSess := session.NewSession("app", "session-user", "sess-1")
	blankStateSess.SetState(sessionStateKeyUserID, []byte(" "))
	userID, ok = ResolveUserID(blankStateSess, nil)
	require.True(t, ok)
	require.Equal(t, "session-user", userID)

	userID, ok = ResolveUserID(
		session.NewSession("app", " ", "sess-1"),
		map[string]any{runtimeStateKeyUserID: " "},
	)
	require.False(t, ok)
	require.Empty(t, userID)
}

func TestResolveUserKey(t *testing.T) {
	t.Parallel()

	appName, userID, ok := ResolveUserKey(
		session.NewSession("app", "session-user", "sess-1"),
		RuntimeState("runtime-user"),
	)
	require.True(t, ok)
	require.Equal(t, "app", appName)
	require.Equal(t, "runtime-user", userID)

	_, _, ok = ResolveUserKey(nil, nil)
	require.False(t, ok)

	_, _, ok = ResolveUserKey(session.NewSession("", "user", "sess-1"), nil)
	require.False(t, ok)
}

func TestCloneSessionWithRuntimeState(t *testing.T) {
	t.Parallel()

	sess := session.NewSession("app", "session-user", "sess-1")

	cloned := CloneSessionWithRuntimeState(
		sess,
		RuntimeState("runtime-user"),
	)
	require.NotSame(t, sess, cloned)

	userID, ok := ResolveUserID(cloned, nil)
	require.True(t, ok)
	require.Equal(t, "runtime-user", userID)

	originalUserID, ok := ResolveUserID(sess, nil)
	require.True(t, ok)
	require.Equal(t, "session-user", originalUserID)

	require.Same(t, sess, CloneSessionWithRuntimeState(sess, nil))

	cloned = CloneSessionWithRuntimeState(
		sess,
		map[string]any{runtimeStateKeyUserID: 123},
	)
	require.Same(t, sess, cloned)

	require.Nil(t, CloneSessionWithRuntimeState(nil, RuntimeState("runtime-user")))
}

func TestAutoMemoryCursorSessionContext(t *testing.T) {
	t.Parallel()

	sess := session.NewSession("app", "user", "sess-1")

	ctx := ContextWithAutoMemoryCursorSession(context.TODO(), sess)
	cursorSess, ok := AutoMemoryCursorSessionFromContext(ctx)
	require.True(t, ok)
	require.Same(t, sess, cursorSess)

	ctx = ContextWithAutoMemoryCursorSession(context.Background(), nil)
	_, ok = AutoMemoryCursorSessionFromContext(ctx)
	require.False(t, ok)

	_, ok = AutoMemoryCursorSessionFromContext(context.Background())
	require.False(t, ok)

	_, ok = AutoMemoryCursorSessionFromContext(context.TODO())
	require.False(t, ok)
}
