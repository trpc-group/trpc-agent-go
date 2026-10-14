//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package messageorigin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestInputSourceSurvivesPersistence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		current bool
		parent  string
		want    Source
	}{
		{"host turn", true, "", UserInput}, {"imported history", false, "", Unknown}, {"delegated input", true, "parent", Agent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evt := event.New("inv", "user")
			evt.ParentInvocationID = tc.parent
			MarkRunnerInput(context.Background(), evt, model.NewUserMessage("input"), tc.current)
			raw, err := json.Marshal(evt)
			require.NoError(t, err)
			var restored event.Event
			require.NoError(t, json.Unmarshal(raw, &restored))
			require.Equal(t, tc.want, InputSource(&restored))
		})
	}
	require.Equal(t, Unknown, InputSource(event.New("old", "user")))
}

func TestFallbackRunnerInputIsAgentAuthored(t *testing.T) {
	evt := event.New("fallback", "user")
	MarkRunnerInput(WithAgentInput(context.Background()), evt, model.NewUserMessage("delegated"), true)
	require.Equal(t, Agent, InputSource(evt))
}
