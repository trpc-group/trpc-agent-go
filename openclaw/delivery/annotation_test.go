//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package delivery

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeRequestExtensionRoundTrip(t *testing.T) {
	t.Parallel()

	extensions, err := MergeRequestExtension(nil, Target{
		Channel: "wecom",
		Target:  "group:chat1",
	})
	require.NoError(t, err)

	target, ok, err := TargetFromRequestExtensions(extensions)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "wecom", target.Channel)
	require.Equal(t, "group:chat1", target.Target)
}

func TestMergeRequestExtensionSkipsEmptyTarget(t *testing.T) {
	t.Parallel()

	extensions := map[string]json.RawMessage{
		"existing": json.RawMessage(`"value"`),
	}
	got, err := MergeRequestExtension(extensions, Target{})
	require.NoError(t, err)
	require.Equal(t, extensions, got)
}

func TestTargetFromRequestExtensionsRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	_, ok, err := TargetFromRequestExtensions(
		map[string]json.RawMessage{
			extensionKey: json.RawMessage(`{`),
		},
	)
	require.Error(t, err)
	require.False(t, ok)
}
