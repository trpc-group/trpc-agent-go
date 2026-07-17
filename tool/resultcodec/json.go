//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package resultcodec

import (
	"bytes"
	"context"
	"encoding/json"
)

// JSON returns a Codec that encodes the result as JSON, matching the framework's
// default tool result serialization. HTML characters (<, >, &) are not escaped
// so tool output such as source code is preserved verbatim.
func JSON() Codec {
	return jsonCodec{}
}

type jsonCodec struct{}

// Encode serializes the result as JSON without HTML escaping.
func (jsonCodec) Encode(_ context.Context, result any) (string, error) {
	b, err := marshalJSONNoHTMLEscape(result)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// marshalJSONNoHTMLEscape serializes v to JSON without escaping <, >, & and
// reproduces the framework's default tool result encoding byte-for-byte.
// Standard json.Marshal escapes these for HTML safety, but tool results are
// never embedded in HTML and the escaped sequences (\u003c, \u003e, \u0026)
// confuse LLMs that read the output as source code.
func marshalJSONNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// json.Encoder.Encode appends a trailing newline; trim it for Marshal parity.
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}
