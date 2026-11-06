//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package json

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestUnmarshal(t *testing.T) {
	var data map[string]any
	if err := Unmarshal([]byte(`{"id":9007199254740993,"values":[1e400]}`), &data); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": json.Number("9007199254740993"), "values": []any{json.Number("1e400")}}
	if !reflect.DeepEqual(data, want) {
		t.Fatalf("Unmarshal() = %#v, want %#v", data, want)
	}

	for _, raw := range []string{`{"id":1} {"id":2}`, `{"id":1} trailing`, `{invalid}`, ``} {
		t.Run(raw, func(t *testing.T) {
			dest := map[string]any{"original": true}
			if err := Unmarshal([]byte(raw), &dest); err == nil {
				t.Fatal("expected invalid JSON error")
			}
			if !reflect.DeepEqual(dest, map[string]any{"original": true}) {
				t.Fatalf("invalid input mutated destination: %#v", dest)
			}
		})
	}
	var typed struct{ ID int64 }
	if err := Unmarshal([]byte("{\"ID\":9007199254740993} \n\t"), &typed); err != nil {
		t.Fatal(err)
	}
	if typed.ID != 9007199254740993 {
		t.Fatalf("typed ID = %d, want 9007199254740993", typed.ID)
	}
}
