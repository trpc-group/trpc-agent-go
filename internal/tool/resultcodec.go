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

// transparentTool is implemented by wrappers that are safe to traverse when a
// capability is not handled at the current layer. Traversal does not establish a
// global wrapper-precedence rule; each capability resolver defines its own
// behavior. A plain errors.Unwrap() is intentionally NOT followed, so a wrapper
// that renames the tool is not silently stripped and keeps its declaration for
// execution filters.
type transparentTool interface {
	TransparentUnwrap() tool.Tool
}

// resultCodecProvider is the unexported discovery interface implemented by tools
// that carry a result codec. Business code does not implement it directly; it
// configures a codec via function.WithResultCodec or resultcodec.Wrap.
type resultCodecProvider interface {
	ResultCodec() resultcodec.Codec
}

// maxToolUnwrapDepth bounds wrapper-chain traversal so a self-referential or
// mutually cyclic wrapper (for example an extension-provided TransparentUnwrap())
// cannot cause an infinite loop or stack exhaustion.
const maxToolUnwrapDepth = 128

// ResolveResultCodec walks the tool wrapper chain from outermost to innermost
// and returns the first non-nil result codec, or nil when none is configured.
// The traversal is depth-bounded for cycle safety.
func ResolveResultCodec(t tool.Tool) resultcodec.Codec {
	cur := t
	for i := 0; i < maxToolUnwrapDepth && cur != nil; i++ {
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
		case transparentTool:
			cur = w.TransparentUnwrap()
		default:
			return nil
		}
	}
	return nil
}
