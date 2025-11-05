//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package clone provides functions to clone.
package clone

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

// Clone perform deepcopy on src.
func Clone[T any](src *T) (*T, error) {
	if src == nil {
		return nil, fmt.Errorf("nil input")
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(src); err != nil {
		return nil, err
	}
	var dst T
	if err := gob.NewDecoder(&buf).Decode(&dst); err != nil {
		return nil, err
	}
	return &dst, nil
}
