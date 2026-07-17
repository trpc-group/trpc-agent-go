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

// Encode serializes the result as JSON without HTML escaping.
func (jsonCodec) Encode(_ context.Context, result any) (string, error) {
	b, err := jsonx.MarshalNoHTMLEscape(result)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
