//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build !cgo

package sqlitevec

import vecembed "github.com/asg017/sqlite-vec-go-bindings/ncruces"

// NOTE:
// This file only preserves a minimal helper path for builds without cgo.
// It does not provide a full nocgo runtime for knowledge/sqlitevec.
// A non-cgo SQLite driver and sqlite-vec registration path would still be
// required before this backend can run without cgo.

func vecAuto() {}

func vecSerializeFloat32(vector []float32) ([]byte, error) {
	return vecembed.SerializeFloat32(vector)
}
