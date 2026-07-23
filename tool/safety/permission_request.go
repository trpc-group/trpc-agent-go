//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func resolvePermissionDeclaration(
	request *tool.PermissionRequest,
) (declaration *tool.Declaration, err error) {
	if request == nil {
		return nil, errors.New("permission request is nil")
	}
	if request.Tool != nil && isNilValue(request.Tool) {
		return nil, errors.New("permission request tool is nil")
	}
	if request.Declaration != nil {
		return request.Declaration, nil
	}
	if request.Tool == nil {
		return nil, nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			declaration = nil
			err = fmt.Errorf("tool declaration panicked: %v", recovered)
		}
	}()
	declaration = request.Tool.Declaration()
	if declaration == nil {
		return nil, errors.New("tool declaration is nil")
	}
	return declaration, nil
}
