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

func TestNormalizeJSONCanonicalizesFieldOrderAndWhitespace(t *testing.T) {
	// 两条语义相同、仅字段顺序和空白不同的 JSON 必须规范化成同一字符串。
	a := NormalizeJSON([]byte(`{"b":2,"a":1}`))
	b := NormalizeJSON([]byte(`{ "a" : 1 , "b" : 2 }`))
	if a == "" || a != b {
		t.Fatalf("字段顺序/空白不同但语义相同的 JSON 应规范化一致: a=%q b=%q", a, b)
	}
	if a != `{"a":1,"b":2}` {
		t.Fatalf("规范化结果不符合键排序后的紧凑形式: %q", a)
	}
}

func TestNormalizeJSONEmptyAndInvalid(t *testing.T) {
	if got := NormalizeJSON(nil); got != "" {
		t.Fatalf("空输入应返回空串: %q", got)
	}
	if got := NormalizeJSON([]byte("not-json{}")); got != "not-json{}" {
		t.Fatalf("非法 JSON 应原样返回: %q", got)
	}
}
