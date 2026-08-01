//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Compose chains PermissionPolicy checks. The first non-allow decision wins.
// Nil policies are skipped. This lets hosts stack Guard with org-specific
// policies without wrapping ToolSet (and losing Tool / ToolSet capabilities).
func Compose(policies ...tool.PermissionPolicy) tool.PermissionPolicy {
	out := make(composed, 0, len(policies))
	for _, p := range policies {
		if p != nil {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return allowAll{}
	}
	return out
}

type composed []tool.PermissionPolicy

func (c composed) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	for _, p := range c {
		dec, err := p.CheckToolPermission(ctx, req)
		if err != nil {
			return dec, err
		}
		if dec.Action != tool.PermissionActionAllow {
			return dec, nil
		}
	}
	return tool.AllowPermission(), nil
}

type allowAll struct{}

func (allowAll) CheckToolPermission(
	context.Context,
	*tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	return tool.AllowPermission(), nil
}
