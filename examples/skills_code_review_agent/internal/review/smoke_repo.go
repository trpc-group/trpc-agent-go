//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func prepareContainerSmokeRepo(ctx context.Context) (string, func() error, error) {
	dir, err := os.MkdirTemp("", "trpc-code-review-container-smoke-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() error { return os.RemoveAll(dir) }
	if err := writeSmokeRepoBaseline(dir); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	if err := runSmokeGit(ctx, dir, "init", "-q"); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	if err := runSmokeGit(ctx, dir, "add", "."); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	if err := runSmokeGit(ctx, dir, "-c", "user.email=smoke@example.com", "-c", "user.name=Container Smoke", "commit", "-qm", "baseline"); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	if err := writeSmokeRepoChange(dir); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

func writeSmokeRepoBaseline(dir string) error {
	files := map[string]string{
		"go.mod": `module example.com/container-smoke

go 1.23
`,
		"calc/calc.go": `package calc

func Add(a, b int) int {
	return a + b
}
`,
		"calc/calc_test.go": `package calc

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("Add returned unexpected result")
	}
}
`,
	}
	return writeSmokeFiles(dir, files)
}

func writeSmokeRepoChange(dir string) error {
	files := map[string]string{
		"calc/calc.go": `package calc

func Add(a, b int) int {
	return a + b
}

func Double(v int) int {
	return Add(v, v)
}
`,
		"calc/calc_test.go": `package calc

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("Add returned unexpected result")
	}
}

func TestDouble(t *testing.T) {
	if Double(4) != 8 {
		t.Fatal("Double returned unexpected result")
	}
}
`,
	}
	return writeSmokeFiles(dir, files)
}

func writeSmokeFiles(root string, files map[string]string) error {
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func runSmokeGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, out.String())
	}
	return nil
}
