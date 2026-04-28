//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summaryscope

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestScopeFilterKeyHelpers(t *testing.T) {
	t.Run("set and get scope filter key", func(t *testing.T) {
		sess := &session.Session{}
		SetScopeFilterKey(sess, "app/sub")
		require.Equal(t, "app/sub", GetScopeFilterKey(sess))
		require.Equal(t, "app/sub", sess.ServiceMeta[serviceMetaScopeFilterKey])
	})

	t.Run("ignore empty or nil input", func(t *testing.T) {
		require.Equal(t, "", GetScopeFilterKey(nil))

		sess := &session.Session{}
		SetScopeFilterKey(sess, "")
		require.Nil(t, sess.ServiceMeta)
		require.Equal(t, "", GetScopeFilterKey(sess))
	})
}
