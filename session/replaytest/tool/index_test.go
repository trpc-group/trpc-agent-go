//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import "testing"

// 单元测试
// 验证 JSON 对象字段顺序不会影响规范化结果。
func TestNormalizeJSON_IgnoreObjectKeyOrder(t *testing.T) {
	left := NormalizeJSON([]byte(`{"status":"ok","duration_ms":25}`))
	right := NormalizeJSON([]byte(`{"duration_ms":25,"status":"ok"}`))
	if left != right {
		t.Fatalf("JSON 字段顺序不应产生差异: %s != %s", left, right)
	}
}

// 非法 JSON 应原样保留，交给比较器发现差异。
func TestNormalizeJSON_KeepErrorJson(t *testing.T) {
	raw := []byte(`{"broken"`)
	if got := NormalizeJSON(raw); got != string(raw) {
		t.Fatalf("非法 JSON 应保留原值: %q", got)
	}
}
