//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestSessionConfigOptionsFromNewSession(t *testing.T) {
	t.Parallel()

	acpAgent, key := newConfigTestAgent(t, "wait")
	selectOptions, ok := acpAgent.SessionConfigSelectOptions(key)
	require.True(t, ok)
	require.Len(t, selectOptions, 1)
	require.Equal(t, "model", selectOptions[0].ID)
	require.Equal(t, SessionConfigOptionCategoryModel, selectOptions[0].Category)
	require.Equal(t, "model-1", selectOptions[0].CurrentValue)
	require.Equal(t, 0, selectOptions[0].Order)
	require.Len(t, selectOptions[0].Values, 2)

	booleanOptions, ok := acpAgent.SessionConfigBooleanOptions(key)
	require.True(t, ok)
	require.Len(t, booleanOptions, 1)
	require.Equal(t, "read_only", booleanOptions[0].ID)
	require.False(t, booleanOptions[0].CurrentValue)
	require.Equal(t, 1, booleanOptions[0].Order)

	selectOptions[0].Values[0].Name = "mutated"
	again, ok := acpAgent.SessionConfigSelectOptions(key)
	require.True(t, ok)
	require.Equal(t, "Model 1", again[0].Values[0].Name)
}

func TestSessionConfigOptionsFromLoadedSession(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithOptions(t, "load_success", io.Discard, false)
	service := inmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "config-load",
	}
	invocation := newSessionInvocation(t, service, key)
	setPersistedSessionState(
		t,
		acpAgent,
		invocation.Session,
		acpsdk.SessionId("persisted-session"),
	)

	_, err := acpAgent.acquireSession(context.Background(), invocation)
	require.NoError(t, err)
	options, ok := acpAgent.SessionConfigSelectOptions(key)
	require.True(t, ok)
	require.Equal(t, "model-1", options[0].CurrentValue)
}

func TestSessionConfigGroupedSelectValues(t *testing.T) {
	t.Parallel()

	values := acpsdk.SessionConfigSelectOptionsGrouped{
		{
			Group: "recommended",
			Name:  "Recommended",
			Options: []acpsdk.SessionConfigSelectOption{
				{Value: "model-1", Name: "Model 1"},
			},
		},
		{
			Group: "other",
			Name:  "Other",
			Options: []acpsdk.SessionConfigSelectOption{
				{Value: "model-2", Name: "Model 2"},
			},
		},
	}
	config := newSessionConfiguration([]acpsdk.SessionConfigOption{
		{
			Select: &acpsdk.SessionConfigOptionSelect{
				Id:           "model",
				Name:         "Model",
				CurrentValue: "model-1",
				Options: acpsdk.SessionConfigSelectOptions{
					Grouped: &values,
				},
			},
		},
	})
	options := config.selectOptions()
	require.Len(t, options, 1)
	require.Equal(t, []SessionConfigSelectValue{
		{
			Value:     "model-1",
			Name:      "Model 1",
			Group:     "recommended",
			GroupName: "Recommended",
		},
		{
			Value:     "model-2",
			Name:      "Model 2",
			Group:     "other",
			GroupName: "Other",
		},
	}, options[0].Values)
}

func TestSessionConfigOptionsWithoutBinding(t *testing.T) {
	t.Parallel()

	var acpAgent *ACPAgent
	_, ok := acpAgent.SessionConfigSelectOptions(session.Key{})
	require.False(t, ok)

	acpAgent, _ = newConfigTestAgent(t, "wait")
	_, ok = acpAgent.SessionConfigBooleanOptions(session.Key{
		AppName:   "missing",
		UserID:    "missing",
		SessionID: "missing",
	})
	require.False(t, ok)
}

func TestSetSessionConfigSelectOption(t *testing.T) {
	t.Parallel()

	acpAgent, key := newConfigTestAgent(t, "wait")
	require.NoError(t, acpAgent.SetSessionConfigSelectOption(
		context.Background(),
		key,
		"model",
		"model-2",
	))

	options, ok := acpAgent.SessionConfigSelectOptions(key)
	require.True(t, ok)
	require.Equal(t, "model-2", options[0].CurrentValue)

	err := acpAgent.SetSessionConfigSelectOption(
		context.Background(),
		key,
		"model",
		"missing",
	)
	require.ErrorIs(t, err, ErrSessionConfigValueNotAllowed)

	err = acpAgent.SetSessionConfigSelectOption(
		context.Background(),
		key,
		"missing",
		"model-1",
	)
	require.ErrorIs(t, err, ErrSessionConfigOptionNotFound)

	err = acpAgent.SetSessionConfigSelectOption(
		context.Background(),
		key,
		"read_only",
		"true",
	)
	require.ErrorIs(t, err, ErrSessionConfigOptionTypeMismatch)
}

func TestSetSessionConfigBooleanOption(t *testing.T) {
	t.Parallel()

	acpAgent, key := newConfigTestAgent(t, "wait")
	require.NoError(t, acpAgent.SetSessionConfigBooleanOption(
		context.Background(),
		key,
		"read_only",
		true,
	))

	options, ok := acpAgent.SessionConfigBooleanOptions(key)
	require.True(t, ok)
	require.True(t, options[0].CurrentValue)

	require.NoError(t, acpAgent.SetSessionConfigBooleanOption(
		context.Background(),
		key,
		"read_only",
		false,
	))
	options, ok = acpAgent.SessionConfigBooleanOptions(key)
	require.True(t, ok)
	require.False(t, options[0].CurrentValue)

	err := acpAgent.SetSessionConfigBooleanOption(
		context.Background(),
		key,
		"model",
		true,
	)
	require.ErrorIs(t, err, ErrSessionConfigOptionTypeMismatch)
}

func TestSetSessionConfigOptionErrorKeepsCachedValue(t *testing.T) {
	t.Parallel()

	acpAgent, key := newConfigTestAgent(t, "set_config_error")
	err := acpAgent.SetSessionConfigSelectOption(
		context.Background(),
		key,
		"model",
		"model-2",
	)
	require.ErrorContains(t, err, "set config failed")

	options, ok := acpAgent.SessionConfigSelectOptions(key)
	require.True(t, ok)
	require.Equal(t, "model-1", options[0].CurrentValue)
}

func TestSetSessionConfigOptionWithoutBinding(t *testing.T) {
	t.Parallel()

	acpAgent, _ := newConfigTestAgent(t, "wait")
	err := acpAgent.SetSessionConfigSelectOption(
		context.Background(),
		session.Key{
			AppName:   "missing",
			UserID:    "missing",
			SessionID: "missing",
		},
		"model",
		"model-2",
	)
	require.ErrorIs(t, err, ErrSessionNotActive)
}

func TestSetSessionConfigOptionAfterCloseIsInactive(t *testing.T) {
	t.Parallel()

	acpAgent, key := newConfigTestAgent(t, "wait")
	require.NoError(t, acpAgent.CloseSession(context.Background(), key))
	err := acpAgent.SetSessionConfigBooleanOption(
		context.Background(),
		key,
		"read_only",
		true,
	)
	require.ErrorIs(t, err, ErrSessionNotActive)
}

func TestSessionConfigUpdateCachesWithoutActiveTurn(t *testing.T) {
	t.Parallel()

	acpAgent, key := newConfigTestAgent(t, "wait")
	binding := acpAgent.sessions.lookup(key)
	require.NotNil(t, binding)

	client := &protocolClient{
		activeTurns: acpAgent.activeTurns,
		sessions:    acpAgent.sessions,
	}
	err := client.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: binding.id,
		Update: acpsdk.SessionUpdate{
			ConfigOptionUpdate: &acpsdk.SessionConfigOptionUpdate{
				ConfigOptions: helperSessionConfigOptions("model-2", true),
			},
		},
	})
	require.NoError(t, err)

	selectOptions, ok := acpAgent.SessionConfigSelectOptions(key)
	require.True(t, ok)
	require.Equal(t, "model-2", selectOptions[0].CurrentValue)
	booleanOptions, ok := acpAgent.SessionConfigBooleanOptions(key)
	require.True(t, ok)
	require.True(t, booleanOptions[0].CurrentValue)
}

func TestSessionConfigUpdateProducesExtensionEvent(t *testing.T) {
	t.Parallel()

	translator := newSessionUpdateTranslator("invocation", "agent")
	events, err := translator.translate(acpsdk.SessionNotification{
		Update: acpsdk.SessionUpdate{
			ConfigOptionUpdate: &acpsdk.SessionConfigOptionUpdate{
				ConfigOptions: helperSessionConfigOptions("model-2", true),
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)

	raw, ok := events[0].Extensions[acpSessionConfigExtension]
	require.True(t, ok)
	var snapshot sessionConfigSnapshot
	require.NoError(t, json.Unmarshal(raw, &snapshot))
	require.Equal(t, "model-2", snapshot.SelectOptions[0].CurrentValue)
	require.True(t, snapshot.BooleanOptions[0].CurrentValue)
}

func TestSetSessionConfigOptionRestoresRecycledProcess(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithConfig(
		t,
		"resume_success",
		io.Discard,
		false,
		func(config *processConfig) {
			config.IdleTimeout = 100 * time.Millisecond
		},
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "config-recycle",
	}
	_, err := acpAgent.acquireSession(
		context.Background(),
		newSessionInvocation(t, service, key),
	)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return snapshotConnectionManager(acpAgent.connections).state == lifecycleStopped
	}, 3*time.Second, 10*time.Millisecond)

	require.NoError(t, acpAgent.SetSessionConfigSelectOption(
		context.Background(),
		key,
		"model",
		"model-2",
	))
	options, ok := acpAgent.SessionConfigSelectOptions(key)
	require.True(t, ok)
	require.Equal(t, "model-2", options[0].CurrentValue)
	require.Equal(t, lifecycleRunning,
		snapshotConnectionManager(acpAgent.connections).state)
}

func TestSetSessionConfigOptionUnknownRestoredSessionIsInactive(t *testing.T) {
	t.Parallel()

	acpAgent := newHelperAgentWithConfig(
		t,
		"resume_unknown",
		io.Discard,
		false,
		func(config *processConfig) {
			config.IdleTimeout = 100 * time.Millisecond
		},
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "config-unknown",
	}
	_, err := acpAgent.acquireSession(
		context.Background(),
		newSessionInvocation(t, service, key),
	)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return snapshotConnectionManager(acpAgent.connections).state == lifecycleStopped
	}, 3*time.Second, 10*time.Millisecond)

	err = acpAgent.SetSessionConfigSelectOption(
		context.Background(),
		key,
		"model",
		"model-2",
	)
	require.ErrorIs(t, err, ErrSessionNotActive)
	_, ok := acpAgent.SessionConfigSelectOptions(key)
	require.False(t, ok)
}

func TestPromptConfigUpdateIsCachedAndEmitted(t *testing.T) {
	t.Parallel()

	acpAgent, key := newConfigTestAgent(t, "prompt_config_update")
	service := inmemory.NewSessionService()
	invocation := newSessionInvocation(t, service, key)
	invocation.Message = model.NewUserMessage("hello")

	events, err := acpAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	var updateFound bool
	for event := range events {
		if event == nil {
			continue
		}
		if _, ok := event.Extensions[acpSessionConfigExtension]; ok {
			updateFound = true
		}
	}
	require.True(t, updateFound)

	options, ok := acpAgent.SessionConfigSelectOptions(key)
	require.True(t, ok)
	require.Equal(t, "model-2", options[0].CurrentValue)
}

func newConfigTestAgent(t *testing.T, mode string) (*ACPAgent, session.Key) {
	t.Helper()
	acpAgent := newHelperAgentWithOptions(
		t,
		mode,
		io.Discard,
		false,
		WithAuthMethodSelectorFunc(agentLoginSelector()),
	)
	service := inmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "config-" + mode,
	}
	_, err := acpAgent.acquireSession(
		context.Background(),
		newSessionInvocation(t, service, key),
	)
	require.NoError(t, err)
	return acpAgent, key
}
