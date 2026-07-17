//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package jsonx provides small, dependency-free JSON encoding helpers shared
// across the framework. It imports only the standard library so any package can
// depend on it without creating import cycles.
package jsonx

import (
	"bytes"
	"encoding/json"
)

// MarshalNoHTMLEscape serializes v to JSON without escaping <, >, & characters.
//
// Standard json.Marshal escapes these for HTML safety, but tool results and
// similar model-visible payloads are never embedded in HTML, and the escaped
// sequences (\u003c, \u003e, \u0026) confuse LLMs that read the output as source
// code (for example Go channel operations "<-done"). The trailing newline that
// json.Encoder appends is trimmed for json.Marshal parity.
//
// This is the single source of truth for the framework's default tool result
// encoding; keep behavior identical across callers.
func MarshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}
