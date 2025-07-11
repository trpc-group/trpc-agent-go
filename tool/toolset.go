//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package tool

import "context"

// ToolSet defines an interface for managing a set of tools.
// It provides methods to retrieve the current tools and to perform cleanup.
type ToolSet interface {
	// Tools returns a slice of CallableTool instances available in the set based on the provided context.
	Tools(context.Context) []CallableTool

	// Close releases any resources held by the ToolSet.
	Close() error
}
