//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package registry provides pluggable registries for OpenClaw demo
// components.
//
// The intent is to make it easy for downstream repos (for example an
// internal distribution) to inject enterprise-only capabilities by
// using anonymous imports:
//
//	import (
//		_ "your/module/openclaw_plugins/wecom"
//		_ "your/module/openclaw_plugins/postgres"
//	)
//
// Each plugin package registers factories in init().
package registry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memextractor "trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
)

const (
	// ModelTypeMock is the built-in mock model type.
	ModelTypeMock = "mock"
	// ModelTypeOpenAI is the built-in OpenAI-compatible model type.
	ModelTypeOpenAI = "openai"

	// BackendInMemory is the built-in in-memory backend type.
	BackendInMemory = "inmemory"
	// BackendRedis is the built-in Redis backend type.
	BackendRedis = "redis"

	// ChannelTypeTelegram is the built-in Telegram channel type.
	ChannelTypeTelegram = "telegram"
)

// GatewayClient is the minimal gateway client surface a channel needs.
type GatewayClient interface {
	SendMessage(
		ctx context.Context,
		req gwclient.MessageRequest,
	) (gwclient.MessageResponse, error)

	Cancel(ctx context.Context, requestID string) (bool, error)
}

// ChannelDeps are dependencies passed to channel factories.
type ChannelDeps struct {
	Gateway  GatewayClient
	StateDir string
	AppName  string
}

// PluginSpec describes one configured plugin instance.
type PluginSpec struct {
	Type   string
	Name   string
	Config *yaml.Node
}

// ChannelFactory creates a channel from config.
type ChannelFactory func(
	deps ChannelDeps,
	spec PluginSpec,
) (channel.Channel, error)

// SessionDeps are dependencies passed to session backend factories.
type SessionDeps struct {
	Model      model.Model
	Summarizer summary.SessionSummarizer
	AppName    string
}

// RedisSpec describes the common Redis options in OpenClaw demo configs.
type RedisSpec struct {
	URL       string
	Instance  string
	KeyPrefix string
}

// SessionBackendSpec describes which session backend to create.
type SessionBackendSpec struct {
	Type   string
	Redis  RedisSpec
	Config *yaml.Node
}

// SessionBackendFactory creates a session service backend.
type SessionBackendFactory func(
	deps SessionDeps,
	spec SessionBackendSpec,
) (session.Service, error)

// MemoryDeps are dependencies passed to memory backend factories.
type MemoryDeps struct {
	Model     model.Model
	Extractor memextractor.MemoryExtractor
	AppName   string
}

// MemoryBackendSpec describes which memory backend to create.
type MemoryBackendSpec struct {
	Type   string
	Redis  RedisSpec
	Limit  int
	Config *yaml.Node
}

// MemoryBackendFactory creates a memory service backend.
type MemoryBackendFactory func(
	deps MemoryDeps,
	spec MemoryBackendSpec,
) (memory.Service, error)

// ToolProviderDeps are dependencies passed to tool provider factories.
type ToolProviderDeps struct {
	Model    model.Model
	StateDir string
	AppName  string
}

// ToolProviderFactory creates additional tools for an agent.
type ToolProviderFactory func(
	deps ToolProviderDeps,
	spec PluginSpec,
) ([]tool.Tool, error)

// ToolSetProviderDeps are dependencies passed to tool set factories.
type ToolSetProviderDeps struct {
	Model    model.Model
	StateDir string
	AppName  string
}

// ToolSetProviderFactory creates a ToolSet (a dynamic tool collection).
type ToolSetProviderFactory func(
	deps ToolSetProviderDeps,
	spec PluginSpec,
) (tool.ToolSet, error)

// ModelSpec describes which model to create.
type ModelSpec struct {
	Type          string
	Name          string
	BaseURL       string
	OpenAIVariant string

	Config *yaml.Node
}

// ModelFactory creates a model from config.
type ModelFactory func(spec ModelSpec) (model.Model, error)

var (
	mu sync.RWMutex

	channelFactories = map[string]ChannelFactory{}
	sessionFactories = map[string]SessionBackendFactory{}
	memoryFactories  = map[string]MemoryBackendFactory{}
	toolFactories    = map[string]ToolProviderFactory{}
	toolSetFactories = map[string]ToolSetProviderFactory{}
	modelFactories   = map[string]ModelFactory{}
)

// RegisterChannel registers a channel factory under typeName.
func RegisterChannel(typeName string, f ChannelFactory) error {
	name, err := validateType("channel", typeName)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("registry: channel factory is nil: %s", name)
	}

	mu.Lock()
	defer mu.Unlock()
	if _, ok := channelFactories[name]; ok {
		return fmt.Errorf("registry: channel already registered: %s", name)
	}
	channelFactories[name] = f
	return nil
}

// LookupChannel returns a channel factory by typeName.
func LookupChannel(typeName string) (ChannelFactory, bool) {
	mu.RLock()
	defer mu.RUnlock()
	name := normalizeType(typeName)
	f, ok := channelFactories[name]
	return f, ok
}

// RegisterSessionBackend registers a session backend factory under typeName.
func RegisterSessionBackend(typeName string, f SessionBackendFactory) error {
	name, err := validateType("session backend", typeName)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf(
			"registry: session backend factory is nil: %s",
			name,
		)
	}

	mu.Lock()
	defer mu.Unlock()
	if _, ok := sessionFactories[name]; ok {
		return fmt.Errorf(
			"registry: session backend already registered: %s",
			name,
		)
	}
	sessionFactories[name] = f
	return nil
}

// LookupSessionBackend returns a session backend factory by typeName.
func LookupSessionBackend(typeName string) (SessionBackendFactory, bool) {
	mu.RLock()
	defer mu.RUnlock()
	name := normalizeType(typeName)
	f, ok := sessionFactories[name]
	return f, ok
}

// RegisterMemoryBackend registers a memory backend factory under typeName.
func RegisterMemoryBackend(typeName string, f MemoryBackendFactory) error {
	name, err := validateType("memory backend", typeName)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf(
			"registry: memory backend factory is nil: %s",
			name,
		)
	}

	mu.Lock()
	defer mu.Unlock()
	if _, ok := memoryFactories[name]; ok {
		return fmt.Errorf(
			"registry: memory backend already registered: %s",
			name,
		)
	}
	memoryFactories[name] = f
	return nil
}

// LookupMemoryBackend returns a memory backend factory by typeName.
func LookupMemoryBackend(typeName string) (MemoryBackendFactory, bool) {
	mu.RLock()
	defer mu.RUnlock()
	name := normalizeType(typeName)
	f, ok := memoryFactories[name]
	return f, ok
}

// RegisterToolProvider registers a tool provider factory under typeName.
func RegisterToolProvider(typeName string, f ToolProviderFactory) error {
	name, err := validateType("tool provider", typeName)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf(
			"registry: tool provider factory is nil: %s",
			name,
		)
	}

	mu.Lock()
	defer mu.Unlock()
	if _, ok := toolFactories[name]; ok {
		return fmt.Errorf(
			"registry: tool provider already registered: %s",
			name,
		)
	}
	toolFactories[name] = f
	return nil
}

// LookupToolProvider returns a tool provider factory by typeName.
func LookupToolProvider(typeName string) (ToolProviderFactory, bool) {
	mu.RLock()
	defer mu.RUnlock()
	name := normalizeType(typeName)
	f, ok := toolFactories[name]
	return f, ok
}

// RegisterToolSetProvider registers a tool set factory under typeName.
func RegisterToolSetProvider(
	typeName string,
	f ToolSetProviderFactory,
) error {
	name, err := validateType("toolset provider", typeName)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("registry: toolset factory is nil: %s", name)
	}

	mu.Lock()
	defer mu.Unlock()
	if _, ok := toolSetFactories[name]; ok {
		return fmt.Errorf(
			"registry: toolset provider already registered: %s",
			name,
		)
	}
	toolSetFactories[name] = f
	return nil
}

// LookupToolSetProvider returns a tool set factory by typeName.
func LookupToolSetProvider(
	typeName string,
) (ToolSetProviderFactory, bool) {
	mu.RLock()
	defer mu.RUnlock()
	name := normalizeType(typeName)
	f, ok := toolSetFactories[name]
	return f, ok
}

// RegisterModel registers a model factory under typeName.
func RegisterModel(typeName string, f ModelFactory) error {
	name, err := validateType("model", typeName)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("registry: model factory is nil: %s", name)
	}

	mu.Lock()
	defer mu.Unlock()
	if _, ok := modelFactories[name]; ok {
		return fmt.Errorf("registry: model already registered: %s", name)
	}
	modelFactories[name] = f
	return nil
}

// LookupModel returns a model factory by typeName.
func LookupModel(typeName string) (ModelFactory, bool) {
	mu.RLock()
	defer mu.RUnlock()
	name := normalizeType(typeName)
	f, ok := modelFactories[name]
	return f, ok
}

func normalizeType(typeName string) string {
	return strings.ToLower(strings.TrimSpace(typeName))
}

func validateType(kind string, typeName string) (string, error) {
	name := normalizeType(typeName)
	if name == "" {
		return "", fmt.Errorf("registry: %s type name is empty", kind)
	}
	return name, nil
}

// Types returns a sorted list of registered types for a registry kind.
func Types(kind string) []string {
	mu.RLock()
	defer mu.RUnlock()

	var keys []string
	switch normalizeType(kind) {
	case "channel":
		keys = mapKeys(channelFactories)
	case "session backend":
		keys = mapKeys(sessionFactories)
	case "memory backend":
		keys = mapKeys(memoryFactories)
	case "tool provider":
		keys = mapKeys(toolFactories)
	case "toolset provider":
		keys = mapKeys(toolSetFactories)
	case "model":
		keys = mapKeys(modelFactories)
	default:
		return nil
	}
	sort.Strings(keys)
	return keys
}

func mapKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// DecodeStrict decodes cfg into out and rejects unknown YAML fields.
func DecodeStrict(cfg *yaml.Node, out any) error {
	if out == nil {
		return errors.New("registry: nil decode target")
	}
	if cfg == nil || cfg.Kind == 0 {
		return nil
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("registry: marshal config: %w", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return err
	}

	var extra any
	if err := dec.Decode(&extra); err == nil && extra != nil {
		return errors.New(
			"registry: multiple YAML documents are not supported",
		)
	}
	return nil
}
