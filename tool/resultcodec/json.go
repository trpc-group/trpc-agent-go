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
	"context"

	"trpc.group/trpc-go/trpc-agent-go/internal/jsonx"
)

// JSON returns a Codec that encodes the result as JSON, matching the framework's
// default tool result serialization. HTML characters (<, >, &) are not escaped
// so tool output such as source code is preserved verbatim.
func JSON() Codec {
	return jsonCodec{}
}

type jsonCodec struct{}

// Encode serializes the result as JSON without HTML escaping. A panic from a
// business MarshalJSON is recovered and returned as an error.
func (jsonCodec) Encode(_ context.Context, result any) (s string, err error) {
	defer recoverCodecPanic("JSON", &err)
	b, mErr := jsonx.MarshalNoHTMLEscape(result)
	if mErr != nil {
		return "", mErr
	}
	return string(b), nil
}
