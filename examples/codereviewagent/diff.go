//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var hunkPattern = regexp.MustCompile(`^@@ -[0-9]+(?:,[0-9]+)? \+([0-9]+)(?:,[0-9]+)? @@`)

func loadDiff(ctx context.Context, diffFile, repoPath string) ([]byte, string, error) {
	switch {
	case strings.TrimSpace(repoPath) != "":
		runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		command := exec.CommandContext(runCtx, "git", "-C", repoPath, "diff", "--no-ext-diff", "--unified=0", "HEAD")
		data, err := command.Output()
		if err != nil {
			return nil, "", fmt.Errorf("read repository diff: %w", err)
		}
		return data, repoPath, nil
	case strings.TrimSpace(diffFile) != "":
		data, err := os.ReadFile(diffFile)
		if err != nil {
			return nil, "", fmt.Errorf("read diff file: %w", err)
		}
		return data, diffFile, nil
	default:
		return nil, "", errors.New("diff-file or repo-path is required")
	}
}

func parseUnifiedDiff(data []byte) ([]changedLine, error) {
	var result []changedLine
	file := ""
	newLine := 0
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "+++ "):
			file = strings.TrimPrefix(line, "+++ ")
			file = strings.TrimPrefix(file, "b/")
		case strings.HasPrefix(line, "@@ "):
			matches := hunkPattern.FindStringSubmatch(line)
			if len(matches) != 2 {
				return nil, fmt.Errorf("parse hunk header %q", line)
			}
			value, err := strconv.Atoi(matches[1])
			if err != nil {
				return nil, fmt.Errorf("parse hunk line: %w", err)
			}
			newLine = value
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			if file != "" {
				result = append(result, changedLine{File: file, Line: newLine, Content: strings.TrimPrefix(line, "+")})
			}
			newLine++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			// Removed lines do not advance the new-file line number.
		case strings.HasPrefix(line, " "):
			newLine++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan diff: %w", err)
	}
	if len(result) == 0 {
		return nil, errors.New("diff has no added lines")
	}
	return result, nil
}
