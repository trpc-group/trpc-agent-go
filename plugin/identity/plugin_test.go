//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func requireBeforeAgent(t *testing.T, p *Plugin, inv *agent.Invocation) {
	t.Helper()
	_, err := p.beforeAgent(
		context.Background(),
		&agent.BeforeAgentArgs{Invocation: inv},
	)
	require.NoError(t, err)
}

func TestPlugin_Name(t *testing.T) {
	p := NewPlugin(nil)
	assert.Equal(t, "identity", p.Name())

	p2 := NewNamedPlugin("my-auth", nil)
	assert.Equal(t, "my-auth", p2.Name())
}

func TestPlugin_ImplementsInterface(t *testing.T) {
	var _ plugin.Plugin = (*Plugin)(nil)
}

func TestIdentity_ContextRoundTrip(t *testing.T) {
	id := &Identity{UserID: "eve", Token: "t"}
	var nilCtx context.Context
	ctx := NewContext(nilCtx, id)
	got, ok := FromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, "eve", got.UserID)

	_, ok = FromContext(context.Background())
	assert.False(t, ok)

	var nilFromCtx context.Context
	_, ok = FromContext(nilFromCtx)
	assert.False(t, ok)

	// NewContext with a nil identity must be normalized on read to
	// (nil, false) rather than leaking a typed-nil pointer with ok==true.
	ctxNilID := NewContext(context.Background(), nil)
	gotNil, okNil := FromContext(ctxNilID)
	assert.Nil(t, gotNil)
	assert.False(t, okNil)
}

func TestIdentity_HeadersAndEnvFromContext(t *testing.T) {
	id := &Identity{
		Headers: map[string]string{"Authorization": "Bearer tok"},
		EnvVars: map[string]string{"USER_ACCESS_TOKEN": "user-token"},
	}
	ctx := NewContext(context.Background(), id)

	headers, err := HeadersFromContext(ctx)
	require.NoError(t, err)
	require.Equal(t, "Bearer tok", headers["Authorization"])
	headers["Authorization"] = "mutated"
	require.Equal(t, "Bearer tok", id.Headers["Authorization"])

	env := EnvVarsFromContext(ctx)
	require.Equal(t, "user-token", env["USER_ACCESS_TOKEN"])
	env["USER_ACCESS_TOKEN"] = "mutated"
	require.Equal(t, "user-token", id.EnvVars["USER_ACCESS_TOKEN"])

	headers, err = HeadersFromContext(context.Background())
	require.NoError(t, err)
	require.Nil(t, headers)
	require.Nil(t, EnvVarsFromContext(context.Background()))
}

func TestPlugin_BeforeAgent_ResolvesIdentity(t *testing.T) {
	resolved := &Identity{
		UserID: "alice",
		Token:  "tok-123",
		Headers: map[string]string{
			"Authorization": "Bearer tok-123",
		},
		EnvVars: map[string]string{
			"BIZ_TOKEN": "tok-123",
		},
	}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		assert.Equal(t, "alice", uid)
		assert.Equal(t, "sess-1", sid)
		return resolved, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{
			UserID: "alice",
			ID:     "sess-1",
		},
	}

	_, err := p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{
		Invocation: inv,
	})
	require.NoError(t, err)

	val, ok := inv.GetState(stateKey)
	require.True(t, ok)
	got := val.(*Identity)
	assert.Equal(t, "alice", got.UserID)
	assert.Equal(t, "tok-123", got.Token)
}

func TestPlugin_BeforeAgent_ReusesExistingIdentity(t *testing.T) {
	called := false
	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		called = true
		return &Identity{UserID: uid}, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "alice", ID: "sess-1"},
	}
	inv.SetState(stateKey, &Identity{UserID: "already-set"})

	_, err := p.beforeAgent(
		context.Background(),
		&agent.BeforeAgentArgs{Invocation: inv},
	)
	require.NoError(t, err)
	require.False(t, called)

	val, ok := inv.GetState(stateKey)
	require.True(t, ok)
	require.Equal(t, "already-set", val.(*Identity).UserID)
}

func TestPlugin_BeforeAgent_NilProvider(t *testing.T) {
	p := NewPlugin(nil)
	inv := &agent.Invocation{
		Session: &session.Session{UserID: "x", ID: "s"},
	}
	requireBeforeAgent(t, p, inv)
}

func TestPlugin_BeforeAgent_NilArgs(t *testing.T) {
	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		t.Fatal("should not be called")
		return nil, nil
	}))
	_, err := p.beforeAgent(context.Background(), nil)
	require.NoError(t, err)
}

func TestPlugin_BeforeTool_InjectsContext(t *testing.T) {
	id := &Identity{UserID: "bob", Token: "tok-456"}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "bob", ID: "s1"},
	}
	requireBeforeAgent(t, p, inv)

	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "calculator",
		Arguments: []byte(`{"a":1}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Context)

	got, ok := FromContext(result.Context)
	require.True(t, ok)
	assert.Equal(t, "bob", got.UserID)
}

func TestPlugin_BeforeTool_ContextCarriesEnvVars(t *testing.T) {
	id := &Identity{
		UserID:  "charlie",
		EnvVars: map[string]string{"USER_ACCESS_TOKEN": "user-999", "BIZ_USER_ID": "charlie"},
	}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "charlie", ID: "s2"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"ls -la","workdir":"/tmp"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.ModifiedArguments)
	require.Equal(
		t,
		id.EnvVars,
		EnvVarsFromContext(result.Context),
	)
}

func TestPlugin_BeforeTool_NilArgs(t *testing.T) {
	p := NewPlugin(nil)
	result, err := p.beforeTool(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestPlugin_BeforeTool_NoInvocationInContext(t *testing.T) {
	p := NewPlugin(nil)
	result, err := p.beforeTool(context.Background(), &tool.BeforeToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"pwd"}`),
	})
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestPlugin_BeforeTool_NoModificationForNonExecTools(t *testing.T) {
	id := &Identity{UserID: "eve", EnvVars: map[string]string{"TOK": "val"}}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "eve", ID: "s4"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "calculator",
		Arguments: []byte(`{"a":1}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, result.ModifiedArguments)
}

func TestPlugin_BeforeTool_ArgInjection(t *testing.T) {
	id := &Identity{UserID: "frank", Token: "t-frank", Signature: "sig-frank"}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "frank", ID: "s5"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "my_custom_tool",
		Arguments: []byte(`{"query":"hello"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result.ModifiedArguments)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))

	idMap, ok := m["_identity"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "frank", idMap["user_id"])
	assert.Equal(t, "t-frank", idMap["token"])
	assert.Equal(t, "sig-frank", idMap["signature"])
	assert.Equal(t, "hello", m["query"])
}

func TestPlugin_BeforeTool_ContextAndArgInjectionCompose(t *testing.T) {
	id := &Identity{
		UserID:    "harry",
		Token:     "tok-harry",
		Signature: "sig-harry",
		EnvVars:   map[string]string{"USER_ACCESS_TOKEN": "user-harry"},
	}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "harry", ID: "s6"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"env"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ModifiedArguments)
	require.Equal(
		t,
		id.EnvVars,
		EnvVarsFromContext(result.Context),
	)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))
	idMap := m["_identity"].(map[string]any)
	require.Equal(t, "harry", idMap["user_id"])
	require.Equal(t, "tok-harry", idMap["token"])
	require.NotContains(t, m, "env")
}

func TestPlugin_BeforeTool_ArgInjectionSupportsEmptyArguments(t *testing.T) {
	id := &Identity{UserID: "ivy"}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "ivy", ID: "s7"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "my_custom_tool",
		Arguments: nil,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ModifiedArguments)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))
	idMap := m["_identity"].(map[string]any)
	require.Equal(t, "ivy", idMap["user_id"])
	require.NotContains(t, idMap, "token")
	require.NotContains(t, idMap, "signature")
}

func TestPlugin_BeforeTool_ArgInjectionSupportsNullArguments(t *testing.T) {
	id := &Identity{UserID: "jane", Token: "tok-jane", Signature: "sig-jane"}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "jane", ID: "s8"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "my_custom_tool",
		Arguments: []byte(`null`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ModifiedArguments)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))
	idMap := m["_identity"].(map[string]any)
	require.Equal(t, "jane", idMap["user_id"])
	require.Equal(t, "tok-jane", idMap["token"])
	require.Equal(t, "sig-jane", idMap["signature"])
}

func TestPlugin_BeforeTool_ArgInjectionOmitsEmptyFields(t *testing.T) {
	id := &Identity{UserID: "kate"}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "kate", ID: "s9"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "my_custom_tool",
		Arguments: []byte(`{"query":"hello"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ModifiedArguments)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))
	idMap := m["_identity"].(map[string]any)
	require.Equal(t, "kate", idMap["user_id"])
	require.NotContains(t, idMap, "token")
	require.NotContains(t, idMap, "signature")
}

func TestPlugin_BeforeTool_ArgInjectionPreservesLargeIntegers(t *testing.T) {
	id := &Identity{UserID: "luke", Token: "tok-luke"}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "luke", ID: "s10"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "my_custom_tool",
		Arguments: []byte(`{"id":9223372036854775807}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ModifiedArguments)

	dec := json.NewDecoder(bytes.NewReader(result.ModifiedArguments))
	dec.UseNumber()
	var m map[string]any
	require.NoError(t, dec.Decode(&m))
	require.Equal(t, "9223372036854775807", m["id"].(json.Number).String())
}

func TestPlugin_BeforeTool_ArgInjectionSkipsEmptyIdentityPayload(t *testing.T) {
	id := &Identity{
		Headers: map[string]string{"Authorization": "Bearer only-headers"},
		EnvVars: map[string]string{"USER_ACCESS_TOKEN": "only-env"},
	}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "luke", ID: "s10"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "my_custom_tool",
		Arguments: []byte(`{"query":"hello"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.ModifiedArguments)
}

func TestPlugin_BeforeTool_ArgInjectionSkipsReservedIdentityKey(t *testing.T) {
	id := &Identity{UserID: "mike", Token: "tok-mike"}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "mike", ID: "s11"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "my_custom_tool",
		Arguments: []byte(`{"_identity":{"source":"user"}}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.ModifiedArguments)
}

func TestPlugin_BeforeTool_ArgInjectionSkipsNonObjectArguments(t *testing.T) {
	id := &Identity{UserID: "nina", Token: "tok-nina"}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "nina", ID: "s12"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "my_custom_tool",
		Arguments: []byte(`["hello"]`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.ModifiedArguments)
}

func TestPlugin_BeforeTool_ArgInjectionSkipsInvalidJSON(t *testing.T) {
	id := &Identity{UserID: "olivia", Token: "tok-olivia"}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "olivia", ID: "s13"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "my_custom_tool",
		Arguments: []byte(`{`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.ModifiedArguments)
}

func TestPlugin_BeforeTool_NoIdentityInState(t *testing.T) {
	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return nil, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "ghost", ID: "s6"},
	}
	requireBeforeAgent(t, p, inv)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"ls"}`),
	})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestProviderFunc(t *testing.T) {
	called := false
	f := ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		called = true
		return &Identity{UserID: uid}, nil
	})
	id, err := f.Resolve(context.Background(), "u", "s")
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, "u", id.UserID)
}
