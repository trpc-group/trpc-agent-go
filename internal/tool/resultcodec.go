//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultcodec"
)

// toolUnwrapper is implemented by wrappers that expose their underlying tool
// following the errors.Unwrap convention. It lets framework capability checks
// see through wrappers such as resultcodec.Wrap.
type toolUnwrapper interface {
	Unwrap() tool.Tool
}

// resultCodecProvider is the unexported discovery interface implemented by tools
// that carry a result codec. Business code does not implement it directly; it
// configures a codec via function.WithResultCodec or resultcodec.Wrap.
type resultCodecProvider interface {
	ResultCodec() resultcodec.Codec
}

// ResolveResultCodec walks the tool wrapper chain from outermost to innermost
// and returns the first non-nil result codec, or nil when none is configured.
func ResolveResultCodec(t tool.Tool) resultcodec.Codec {
	for cur := t; cur != nil; {
		if p, ok := cur.(resultCodecProvider); ok {
			if c := p.ResultCodec(); c != nil {
				return c
			}
		}
		switch w := cur.(type) {
		case declarationWrapper:
			cur = w.originalTool()
		case *NamedTool:
			cur = w.Original()
		case toolUnwrapper:
			cur = w.Unwrap()
		default:
			cur = nil
		}
	}
	return nil
}
