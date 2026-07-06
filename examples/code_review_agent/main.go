//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func main() {
	var (
		fixtureDir = flag.String("fixture-dir", "testdata/fixtures", "directory containing diff fixtures")
		outDir     = flag.String("out-dir", "./out", "directory for reports and the default SQLite database")
		dbPath     = flag.String("db-path", "", "SQLite database path; defaults to <out-dir>/review_agent.db")
		modelName  = flag.String("model", os.Getenv("MODEL"), "OpenAI-compatible model name")
		runtime    = flag.String("runtime", "container", "workspace runtime: container, e2b, local, or fake")
	)
	flag.Parse()

	_ = context.Background()
	fmt.Printf("Code review agent prototype\n")
	fmt.Printf("- fixture dir: %s\n", *fixtureDir)
	fmt.Printf("- out dir:     %s\n", *outDir)
	if *dbPath != "" {
		fmt.Printf("- db path:     %s\n", *dbPath)
	}
	if *modelName != "" {
		fmt.Printf("- model:       %s\n", *modelName)
	}
	fmt.Printf("- runtime:     %s\n", *runtime)
	fmt.Println("Implementation is wired in subsequent packages.")
}
