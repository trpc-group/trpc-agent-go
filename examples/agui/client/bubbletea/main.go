// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"flag"
	"log"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	endpoint := flag.String("endpoint", "http://localhost:8080/agui", "AG-UI SSE endpoint")
	flag.Parse()
	if _, err := tea.NewProgram(
		initialModel(*endpoint),
		tea.WithAltScreen(),
	).Run(); err != nil {
		log.Fatalf("bubbletea program failed: %v", err)
	}
}
