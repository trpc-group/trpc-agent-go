//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// package main is a example project with bug.
package main

import (
	"log"
	"os"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/filetoolset/project/counter"
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
