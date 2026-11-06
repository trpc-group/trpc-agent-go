//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import "testing"

func TestRedactDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "password is masked",
			dsn:  "clickhouse://user:secret@localhost:9000/default",
			want: "clickhouse://user:****@localhost:9000/default",
		},
		{
			// Splitting on the last colon would leave "pa" visible.
			name: "password containing a colon is fully masked",
			dsn:  "clickhouse://user:pa:ss@localhost:9000/default",
			want: "clickhouse://user:****@localhost:9000/default",
		},
		{
			name: "encoded password is fully masked",
			dsn:  "clickhouse://user:p%40ss%3Aword@localhost:9000/default",
			want: "clickhouse://user:****@localhost:9000/default",
		},
		{
			name: "empty password keeps the user",
			dsn:  "clickhouse://default:@localhost:9000/default",
			want: "clickhouse://default:@localhost:9000/default",
		},
		{
			name: "no credentials is returned unchanged",
			dsn:  "clickhouse://localhost:9000/default",
			want: "clickhouse://localhost:9000/default",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactDSN(tt.dsn); got != tt.want {
				t.Errorf("redactDSN(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}
