//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build cgo

package sqlitevec

import vec "github.com/asg017/sqlite-vec-go-bindings/cgo"

func vecAuto() {
	vec.Auto()
}

func vecSerializeFloat32(vector []float32) ([]byte, error) {
	return vec.SerializeFloat32(vector)
}
