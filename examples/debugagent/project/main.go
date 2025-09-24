//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// package main is a example project with bug.
package main

import (
	"log"
	"os"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/debugagent/project/counter"
)

func main() {
	content, err := os.ReadFile("input.txt")
	if err != nil {
		log.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		log.Fatal(err)
	}
	counter := counter.GetCounter(n)
	os.WriteFile("output.txt", []byte(strconv.Itoa(counter)), 0644)
}
