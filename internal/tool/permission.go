//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"bytes"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// SnapshotPermissionRequest copies the request and its argument bytes so a
// checker cannot rewrite another check's input or the executed arguments.
// Tool and Declaration retain their identity and must be treated as read-only.
// No schema serialization or validation is performed here.
func SnapshotPermissionRequest(req *tool.PermissionRequest) (*tool.PermissionRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("permission request is nil")
	}
	out := *req
	out.Arguments = bytes.Clone(req.Arguments)
	return &out, nil
}
