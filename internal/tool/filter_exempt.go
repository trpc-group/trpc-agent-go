//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import pubtool "trpc.group/trpc-go/trpc-agent-go/tool"

type filterExemptTool interface {
	ExemptFromToolFilter() bool
}

// IsFilterExemptTool reports whether a tool opts out of run-level filtering.
//
// This remains an internal runtime detail so the public tool contract does not
// need an extra marker interface just for DeferredToolSet's search entrypoint.
func IsFilterExemptTool(t pubtool.Tool) bool {
	if t == nil {
		return false
	}
	if named, ok := t.(*NamedTool); ok && named != nil {
		return IsFilterExemptTool(named.original)
	}
	exempt, ok := t.(filterExemptTool)
	return ok && exempt.ExemptFromToolFilter()
}
