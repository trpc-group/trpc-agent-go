//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "testing"

func TestIsLoopbackListenAddress(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{addr: "127.0.0.1:8080", want: true},
		{addr: "localhost:8080", want: true},
		{addr: "[::1]:8080", want: true},
		{addr: ":8080", want: false},
		{addr: "0.0.0.0:8080", want: false},
		{addr: "192.0.2.1:8080", want: false},
		{addr: "invalid", want: false},
	}
	for _, test := range tests {
		t.Run(test.addr, func(t *testing.T) {
			if got := isLoopbackListenAddress(test.addr); got != test.want {
				t.Fatalf(
					"isLoopbackListenAddress(%q) = %v, want %v",
					test.addr,
					got,
					test.want,
				)
			}
		})
	}
}
