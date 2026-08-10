//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolerror

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	schemaResourcePrefix = "https://tool-error.invalid/schema/"
	// defaultSchemaCacheCapacity bounds compiled schemas retained by a
	// runner-scoped plugin while leaving room for large dynamic tool surfaces.
	defaultSchemaCacheCapacity = 256
)

type compiledSchema struct {
	schema *jsonschema.Schema
	err    error
}

type schemaCache struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]compiledSchema
	// order records insertion order for deterministic FIFO eviction.
	order [][sha256.Size]byte
}

func newSchemaCache() schemaCache {
	return schemaCache{
		entries: make(
			map[[sha256.Size]byte]compiledSchema,
			defaultSchemaCacheCapacity,
		),
		order: make([][sha256.Size]byte, 0, defaultSchemaCacheCapacity),
	}
}

func (p *toolErrorPlugin) validateArguments(
	toolName string,
	raw []byte,
	schema *tool.Schema,
) (Details, bool) {
	compiled, err := p.cache.compile(schema)
	if err != nil {
		return Details{
			Source:  SourceFramework,
			Kind:    KindConfiguration,
			Code:    "invalid_schema",
			Message: fmt.Sprintf("invalid input schema for tool %q: %v", toolName, err),
		}, true
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(
		normalizeArguments(raw),
	))
	if err != nil {
		return Details{
			Source:    SourceModel,
			Kind:      KindInvalidArguments,
			Code:      "invalid_json",
			Message:   fmt.Sprintf("invalid JSON arguments: %v", err),
			Retryable: true,
		}, true
	}
	if err := compiled.Validate(value); err != nil {
		return validationDetails(err), true
	}
	return Details{}, false
}

func (c *schemaCache) compile(schema *tool.Schema) (*jsonschema.Schema, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode schema: %w", err)
	}
	key := sha256.Sum256(raw)
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[key]; ok {
		return entry.schema, entry.err
	}
	compiled, err := compileSchema(raw, key)
	if len(c.entries) >= defaultSchemaCacheCapacity {
		oldest := c.order[0]
		delete(c.entries, oldest)
		copy(c.order, c.order[1:])
		c.order = c.order[:len(c.order)-1]
	}
	c.entries[key] = compiledSchema{schema: compiled, err: err}
	c.order = append(c.order, key)
	return compiled, err
}

func compileSchema(
	raw []byte,
	key [sha256.Size]byte,
) (*jsonschema.Schema, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	location := schemaResourcePrefix + hex.EncodeToString(key[:]) + ".json"
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(rejectExternalSchemaLoader{})
	if err := compiler.AddResource(location, document); err != nil {
		return nil, fmt.Errorf("register schema: %w", err)
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return compiled, nil
}

type rejectExternalSchemaLoader struct{}

func (rejectExternalSchemaLoader) Load(location string) (any, error) {
	return nil, fmt.Errorf("external schema reference %q is not allowed", location)
}

func normalizeArguments(raw []byte) []byte {
	if len(bytes.TrimSpace(raw)) > 0 {
		return raw
	}
	return []byte("{}")
}

func validationDetails(err error) Details {
	var validationErr *jsonschema.ValidationError
	if !errors.As(err, &validationErr) {
		return Details{
			Source:    SourceModel,
			Kind:      KindInvalidArguments,
			Code:      "schema",
			Message:   err.Error(),
			Retryable: true,
		}
	}
	leaf := firstValidationLeaf(validationErr)
	path := append([]string(nil), leaf.InstanceLocation...)
	switch errorKind := leaf.ErrorKind.(type) {
	case *kind.Required:
		if len(errorKind.Missing) > 0 {
			path = append(path, errorKind.Missing[0])
		}
	case *kind.AdditionalProperties:
		if len(errorKind.Properties) > 0 {
			path = append(path, errorKind.Properties[0])
		}
	}
	return Details{
		Source:    SourceModel,
		Kind:      KindInvalidArguments,
		Code:      validationCode(leaf),
		Message:   leaf.Error(),
		Param:     jsonPointer(path),
		Retryable: true,
	}
}

func firstValidationLeaf(err *jsonschema.ValidationError) *jsonschema.ValidationError {
	for err != nil && len(err.Causes) > 0 {
		err = err.Causes[0]
	}
	return err
}

func validationCode(err *jsonschema.ValidationError) string {
	if err == nil || err.ErrorKind == nil {
		return "schema"
	}
	path := err.ErrorKind.KeywordPath()
	if len(path) == 0 {
		return "schema"
	}
	return snakeCase(path[len(path)-1])
}

func snakeCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func jsonPointer(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	var b strings.Builder
	for _, token := range tokens {
		b.WriteByte('/')
		token = strings.ReplaceAll(token, "~", "~0")
		token = strings.ReplaceAll(token, "/", "~1")
		b.WriteString(token)
	}
	return b.String()
}

func classifyExecutionError(ctx context.Context, err error) (Details, bool) {
	if err == nil {
		return Details{}, false
	}
	ctxErr := matchingContextError(ctx, err)
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return Details{
			Source:    SourceFramework,
			Kind:      KindExecution,
			Code:      "deadline_exceeded",
			Message:   err.Error(),
			Retryable: true,
		}, true
	}
	if errors.Is(ctxErr, context.Canceled) {
		return Details{
			Source:  SourceFramework,
			Kind:    KindExecution,
			Code:    "canceled",
			Message: err.Error(),
		}, true
	}
	code := "tool_execution"
	if responseError := model.ResponseErrorFromError(err, ""); responseError != nil && responseError.Code != nil &&
		*responseError.Code != "" {
		code = *responseError.Code
	}
	return Details{
		Source:  SourceTool,
		Kind:    KindExecution,
		Code:    code,
		Message: err.Error(),
	}, true
}

func matchingContextError(ctx context.Context, err error) error {
	if ctx == nil || err == nil || ctx.Err() == nil {
		return nil
	}
	ctxErr := ctx.Err()
	if errors.Is(err, ctxErr) {
		return ctxErr
	}
	cause := context.Cause(ctx)
	if cause != nil && errors.Is(err, cause) {
		return ctxErr
	}
	return nil
}

func normalizeDetails(details Details, fallback error) Details {
	if details.Source == "" {
		details.Source = SourceTool
	}
	if details.Kind == "" {
		details.Kind = KindExecution
	}
	if details.Code == "" {
		details.Code = string(details.Kind)
	}
	if details.Message == "" {
		if fallback != nil {
			details.Message = fallback.Error()
		} else {
			details.Message = "tool call failed"
		}
	}
	return details
}
