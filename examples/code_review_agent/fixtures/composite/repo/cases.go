//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package fixture

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"strconv"
)

func Add(left, right int) int { return left + right }

func runCommand(userInput string) error {
	return exec.Command("sh", "-c", userInput).Run()
}

func start(ctx context.Context) {
	child, cancel := context.WithCancel(ctx)
	go func() {
		for {
			work()
		}
	}()
	_ = child
	_ = cancel
}

func work() {}

func read() error {
	file, err := os.Open("data.txt")
	_ = file
	return err
}

func loadValue() int {
	value, _ := strconv.Atoi("1")
	return value
}

func update(db *sql.DB) error {
	tx, err := db.Begin()
	_ = tx
	return err
}

func Enabled() bool { return true }

func duplicateCommand(input string) error {
	return exec.Command("sh", "-c", input).Run()
}

var APIKey = "sk-abcdefghijklmnopqrstuvwxyz123456"
