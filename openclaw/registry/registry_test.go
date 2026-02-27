//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package registry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
)

type dummyToolSet struct{}

func (dummyToolSet) Tools(context.Context) []tool.Tool { return nil }

func (dummyToolSet) Close() error { return nil }

func (dummyToolSet) Name() string { return "dummy" }

type dummyChannel struct{}

func (dummyChannel) ID() string { return "dummy" }

func (dummyChannel) Run(context.Context) error { return nil }

func resetForTest(t *testing.T) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	channelFactories = map[string]ChannelFactory{}
	sessionFactories = map[string]SessionBackendFactory{}
	memoryFactories = map[string]MemoryBackendFactory{}
	toolFactories = map[string]ToolProviderFactory{}
	toolSetFactories = map[string]ToolSetProviderFactory{}
	modelFactories = map[string]ModelFactory{}
}

func TestRegisterAndLookup(t *testing.T) {
	resetForTest(t)

	called := false
	var nilFactory ModelFactory
	require.Error(t, RegisterModel("TeSt", nilFactory))

	err := RegisterModel("TeSt", func(_ ModelSpec) (model.Model, error) {
		called = true
		return nil, nil
	})
	require.NoError(t, err)

	f, ok := LookupModel("test")
	require.True(t, ok)
	_, err = f(ModelSpec{})
	require.NoError(t, err)
	require.True(t, called)

	err = RegisterModel("test", func(_ ModelSpec) (model.Model, error) {
		return nil, nil
	})
	require.Error(t, err)

	var nilToolSetFactory ToolSetProviderFactory
	require.Error(t, RegisterToolSetProvider("TeSt", nilToolSetFactory))

	toolSetCalled := false
	require.NoError(t, RegisterToolSetProvider("TeSt", func(
		_ ToolSetProviderDeps,
		_ PluginSpec,
	) (tool.ToolSet, error) {
		toolSetCalled = true
		return dummyToolSet{}, nil
	}))

	gotTS, ok := LookupToolSetProvider("test")
	require.True(t, ok)
	_, err = gotTS(ToolSetProviderDeps{}, PluginSpec{})
	require.NoError(t, err)
	require.True(t, toolSetCalled)
}

func TestRegisterChannel_ValidatesInputs(t *testing.T) {
	resetForTest(t)

	err := RegisterChannel(
		"",
		func(ChannelDeps, PluginSpec) (channel.Channel, error) {
			return dummyChannel{}, nil
		},
	)
	require.Error(t, err)

	var nilFactory ChannelFactory
	require.Error(t, RegisterChannel("x", nilFactory))

	require.NoError(t, RegisterChannel(
		"TeSt",
		func(ChannelDeps, PluginSpec) (channel.Channel, error) {
			return dummyChannel{}, nil
		},
	))

	_, ok := LookupChannel("test")
	require.True(t, ok)

	require.Error(t, RegisterChannel(
		"test",
		func(ChannelDeps, PluginSpec) (channel.Channel, error) {
			return dummyChannel{}, nil
		},
	))
}

func TestRegisterSessionBackend_ValidatesInputs(t *testing.T) {
	resetForTest(t)

	err := RegisterSessionBackend(
		"",
		func(SessionDeps, SessionBackendSpec) (session.Service, error) {
			return nil, nil
		},
	)
	require.Error(t, err)

	var nilFactory SessionBackendFactory
	require.Error(t, RegisterSessionBackend("x", nilFactory))
}

func TestRegisterAndLookup_MoreKinds(t *testing.T) {
	resetForTest(t)

	sessionCalled := false
	require.NoError(t, RegisterSessionBackend(
		" TeSt ",
		func(_ SessionDeps, _ SessionBackendSpec) (session.Service, error) {
			sessionCalled = true
			return nil, nil
		},
	))

	sessionFactory, ok := LookupSessionBackend("test")
	require.True(t, ok)
	_, err := sessionFactory(SessionDeps{}, SessionBackendSpec{})
	require.NoError(t, err)
	require.True(t, sessionCalled)

	require.Error(t, RegisterSessionBackend(
		"test",
		func(_ SessionDeps, _ SessionBackendSpec) (session.Service, error) {
			return nil, nil
		},
	))

	var nilMemoryFactory MemoryBackendFactory
	require.Error(t, RegisterMemoryBackend("mem", nilMemoryFactory))

	memoryCalled := false
	require.NoError(t, RegisterMemoryBackend(
		"mem",
		func(_ MemoryDeps, _ MemoryBackendSpec) (memory.Service, error) {
			memoryCalled = true
			return nil, nil
		},
	))

	memFactory, ok := LookupMemoryBackend("mem")
	require.True(t, ok)
	_, err = memFactory(MemoryDeps{}, MemoryBackendSpec{})
	require.NoError(t, err)
	require.True(t, memoryCalled)

	providerCalled := false
	require.NoError(t, RegisterToolProvider(
		"p",
		func(_ ToolProviderDeps, _ PluginSpec) ([]tool.Tool, error) {
			providerCalled = true
			return nil, nil
		},
	))

	providerFactory, ok := LookupToolProvider("p")
	require.True(t, ok)
	_, err = providerFactory(ToolProviderDeps{}, PluginSpec{})
	require.NoError(t, err)
	require.True(t, providerCalled)
}

func TestRegisterToolProvider_DuplicateFails(t *testing.T) {
	resetForTest(t)

	require.NoError(t, RegisterToolProvider(
		"p",
		func(ToolProviderDeps, PluginSpec) ([]tool.Tool, error) {
			return nil, nil
		},
	))
	require.Error(t, RegisterToolProvider(
		"p",
		func(ToolProviderDeps, PluginSpec) ([]tool.Tool, error) {
			return nil, nil
		},
	))
}

func TestTypes(t *testing.T) {
	resetForTest(t)

	require.NoError(t, RegisterModel(
		"b",
		func(_ ModelSpec) (model.Model, error) {
			return nil, nil
		},
	))
	require.NoError(t, RegisterModel(
		"a",
		func(_ ModelSpec) (model.Model, error) {
			return nil, nil
		},
	))

	require.Equal(t, []string{"a", "b"}, Types("model"))

	require.NoError(t, RegisterToolSetProvider(
		"b",
		func(_ ToolSetProviderDeps, _ PluginSpec) (tool.ToolSet, error) {
			return nil, nil
		},
	))
	require.NoError(t, RegisterToolSetProvider(
		"a",
		func(_ ToolSetProviderDeps, _ PluginSpec) (tool.ToolSet, error) {
			return nil, nil
		},
	))

	require.Equal(t, []string{"a", "b"}, Types("toolset provider"))

	require.Nil(t, Types("unknown kind"))
}

func TestDecodeStrict(t *testing.T) {
	type cfg struct {
		A string `yaml:"a"`
	}

	var nodeOK yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("a: x"), &nodeOK))

	var got cfg
	require.NoError(t, DecodeStrict(&nodeOK, &got))
	require.Equal(t, "x", got.A)

	var nodeBad yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("b: x"), &nodeBad))

	require.Error(t, DecodeStrict(&nodeBad, &got))
}

func TestDecodeStrict_NilTargetFails(t *testing.T) {
	resetForTest(t)

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("a: x"), &node))

	require.Error(t, DecodeStrict(&node, nil))
}

func TestDecodeStrict_NilConfigNoOp(t *testing.T) {
	resetForTest(t)

	type cfg struct {
		A string `yaml:"a"`
	}
	var got cfg
	require.NoError(t, DecodeStrict(nil, &got))
}

func TestDecodeStrict_KindZeroNoOp(t *testing.T) {
	resetForTest(t)

	type cfg struct {
		A string `yaml:"a"`
	}
	var got cfg
	require.NoError(t, DecodeStrict(&yaml.Node{}, &got))
}

func TestDecodeStrict_MarshalErrorBubblesUp(t *testing.T) {
	resetForTest(t)

	var node yaml.Node
	node.Kind = yaml.AliasNode
	node.Value = "bad"

	var out struct {
		A string `yaml:"a"`
	}
	err := DecodeStrict(&node, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown")
}
