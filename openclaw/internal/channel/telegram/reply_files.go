//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package telegram

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
)

const maxAutoReplyFiles = 16

func (c *Channel) collectReplyFiles(text string) []channel.OutboundFile {
	candidates := replyFileCandidates(text)
	if len(candidates) == 0 {
		return nil
	}

	roots := autoReplyRoots(c.state)
	if len(roots) == 0 {
		return nil
	}

	out := make([]channel.OutboundFile, 0, len(candidates))
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		paths := resolveReplyCandidatePaths(candidate, roots)
		for _, path := range paths {
			clean := filepath.Clean(path)
			if _, ok := seen[clean]; ok {
				continue
			}
			seen[clean] = struct{}{}
			out = append(out, channel.OutboundFile{Path: clean})
			if len(out) >= maxAutoReplyFiles {
				return out
			}
		}
	}
	return out
}

func (c *Channel) sendReplyFiles(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	files []channel.OutboundFile,
) {
	for _, file := range files {
		if err := c.sendFile(ctx, chatID, messageThreadID, file); err != nil {
			log.WarnfContext(
				ctx,
				"telegram: send derived file %q: %v",
				file.Path,
				err,
			)
			return
		}
	}
}

func replyFileCandidates(text string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	appendToken := func(token string) {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			return
		}
		if _, ok := seen[trimmed]; ok {
			return
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}

	for _, match := range telegramInlineCodeRE.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		appendToken(match[1])
	}
	for _, token := range telegramPathTokenRE.FindAllString(text, -1) {
		appendToken(token)
	}
	return out
}

func resolveReplyCandidatePaths(
	token string,
	roots []string,
) []string {
	resolved, err := resolveOutboundFilePath(token)
	if err != nil {
		return nil
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return nil
	}
	abs = filepath.Clean(abs)
	if !pathUnderAnyRoot(abs, roots) {
		return nil
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		return []string{abs}
	}
	return listReplyDirectoryFiles(abs, maxAutoReplyFiles)
}

func listReplyDirectoryFiles(root string, limit int) []string {
	if limit <= 0 {
		return nil
	}

	files := make([]string, 0, 4)
	_ = filepath.WalkDir(
		root,
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d == nil || d.IsDir() {
				return nil
			}
			files = append(files, path)
			if len(files) >= limit {
				return fs.SkipAll
			}
			return nil
		},
	)
	sort.Strings(files)
	return files
}

func autoReplyRoots(stateRoot string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 2)
	appendRoot := func(path string) {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			return
		}
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return
		}
		clean := filepath.Clean(abs)
		if _, ok := seen[clean]; ok {
			return
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}

	appendRoot(stateRoot)
	if cwd, err := os.Getwd(); err == nil {
		appendRoot(cwd)
	}
	return out
}

func pathUnderAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		if pathUnderRoot(path, root) {
			return true
		}
	}
	return false
}
