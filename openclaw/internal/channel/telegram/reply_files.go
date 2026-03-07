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
	"regexp"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

const (
	maxAutoReplyFiles   = 16
	maxReplySearchDepth = 2

	replyTokenTrimPunct = " \t\r\n\"'`[](){}<>"
)

var replyFileExts = map[string]struct{}{
	".avi":  {},
	".csv":  {},
	".doc":  {},
	".docx": {},
	".gif":  {},
	".htm":  {},
	".html": {},
	".jpeg": {},
	".jpg":  {},
	".json": {},
	".m4a":  {},
	".md":   {},
	".mkv":  {},
	".mov":  {},
	".mp3":  {},
	".mp4":  {},
	".oga":  {},
	".ogg":  {},
	".pdf":  {},
	".png":  {},
	".ppt":  {},
	".pptx": {},
	".svg":  {},
	".tar":  {},
	".tgz":  {},
	".tsv":  {},
	".txt":  {},
	".wav":  {},
	".webm": {},
	".webp": {},
	".xls":  {},
	".xlsx": {},
	".xml":  {},
	".yaml": {},
	".yml":  {},
	".zip":  {},
}

var replyPlainFileRE = regexp.MustCompile(
	`[^\s<>()\[\]{}"'` + "`" + `]+\.(?:` +
		`avi|csv|doc|docx|gif|htm|html|jpeg|jpg|json|m4a|` +
		`md|mkv|mov|mp3|mp4|oga|ogg|pdf|png|ppt|pptx|svg|` +
		`tar|tgz|tsv|txt|wav|webm|webp|xls|xlsx|xml|yaml|` +
		`yml|zip)` +
		``,
)

func (c *Channel) collectReplyFiles(
	text string,
	fromID string,
	sessionID string,
) []channel.OutboundFile {
	candidates := replyFileCandidates(text)
	if len(candidates) == 0 {
		return nil
	}

	roots := autoReplyRoots(c.state, fromID, sessionID)
	if len(roots) == 0 {
		return nil
	}

	out := make([]channel.OutboundFile, 0, len(candidates))
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		files := resolveReplyCandidateFiles(candidate, roots)
		for _, file := range files {
			clean := cleanReplyFilePath(file.Path)
			if _, ok := seen[clean]; ok {
				continue
			}
			seen[clean] = struct{}{}
			out = append(out, channel.OutboundFile{
				Path: clean,
				Name: file.Name,
			})
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
		trimmed := cleanReplyCandidateToken(token)
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
	for _, token := range replyPlainFileRE.FindAllString(text, -1) {
		appendToken(token)
	}
	return out
}

func cleanReplyCandidateToken(token string) string {
	core, _ := splitTrailingPathPunct(strings.TrimSpace(token))
	return strings.Trim(core, replyTokenTrimPunct)
}

func looksLikeReplyFileName(token string) bool {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" || strings.Contains(trimmed, "://") {
		return false
	}
	base := filepath.Base(trimmed)
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(base)))
	if ext == "" {
		return false
	}
	if _, ok := replyFileExts[ext]; !ok {
		return false
	}
	name := strings.TrimSpace(strings.TrimSuffix(base, ext))
	return name != ""
}

func resolveReplyCandidateFiles(
	token string,
	roots []string,
) []channel.OutboundFile {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return nil
	}

	if isReplyDirectRef(trimmed) {
		return []channel.OutboundFile{{
			Path: trimmed,
		}}
	}

	if files := resolveReplyExistingPaths(trimmed, roots); len(files) > 0 {
		return files
	}
	if !looksLikeReplyFileName(trimmed) {
		return nil
	}

	return searchReplyNamedFiles(trimmed, roots)
}

func isReplyDirectRef(token string) bool {
	trimmed := strings.TrimSpace(token)
	return strings.HasPrefix(trimmed, fileref.ArtifactPrefix) ||
		strings.HasPrefix(trimmed, fileref.WorkspacePrefix)
}

func resolveReplyExistingPaths(
	token string,
	roots []string,
) []channel.OutboundFile {
	paths := make([]string, 0, len(roots)+1)
	if resolved, err := resolveOutboundFilePath(nil, "", token); err == nil {
		paths = append(paths, resolved)
	}
	if canJoinReplyRoots(token) {
		for _, root := range roots {
			paths = append(paths, filepath.Join(root, token))
		}
	}
	for _, path := range paths {
		files := outboundFilesForPath(path, roots)
		if len(files) > 0 {
			return files
		}
	}
	return nil
}

func canJoinReplyRoots(token string) bool {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" || filepath.IsAbs(trimmed) {
		return false
	}
	if strings.HasPrefix(trimmed, "~") {
		return false
	}
	return !strings.Contains(trimmed, "://")
}

func outboundFilesForPath(
	path string,
	roots []string,
) []channel.OutboundFile {
	abs, info, err := statReplyPath(path)
	if err != nil {
		return nil
	}
	if !pathUnderAnyRoot(abs, roots) {
		return nil
	}
	if !info.IsDir() {
		return []channel.OutboundFile{{Path: abs}}
	}
	paths := listReplyDirectoryFiles(abs, maxAutoReplyFiles)
	out := make([]channel.OutboundFile, 0, len(paths))
	for _, item := range paths {
		out = append(out, channel.OutboundFile{Path: item})
	}
	return out
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

func statReplyPath(path string) (string, os.FileInfo, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", nil, err
	}
	clean := filepath.Clean(abs)
	info, err := os.Stat(clean)
	if err != nil {
		return "", nil, err
	}
	return clean, info, nil
}

func searchReplyNamedFiles(
	token string,
	roots []string,
) []channel.OutboundFile {
	name := filepath.Base(strings.TrimSpace(token))
	if name == "" {
		return nil
	}

	out := make([]channel.OutboundFile, 0, 2)
	seen := make(map[string]struct{})
	for _, root := range roots {
		matches := findReplyNamedFiles(
			root,
			name,
			maxReplySearchDepth,
			maxAutoReplyFiles-len(out),
		)
		for _, match := range matches {
			clean := filepath.Clean(match)
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

func findReplyNamedFiles(
	root string,
	name string,
	maxDepth int,
	limit int,
) []string {
	root = strings.TrimSpace(root)
	if root == "" || limit <= 0 {
		return nil
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	cleanRoot := filepath.Clean(root)
	out := make([]string, 0, 2)
	_ = filepath.WalkDir(
		cleanRoot,
		func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d == nil {
				return nil
			}
			if d.IsDir() {
				if path == cleanRoot {
					return nil
				}
				depth, err := replyPathDepth(cleanRoot, path)
				if err != nil {
					return nil
				}
				if depth > maxDepth {
					return fs.SkipDir
				}
				return nil
			}
			if !matchesReplyFileName(d.Name(), name) {
				return nil
			}
			out = append(out, path)
			if len(out) >= limit {
				return fs.SkipAll
			}
			return nil
		},
	)
	sort.Strings(out)
	return out
}

func replyPathDepth(root string, path string) (int, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return 0, err
	}
	if rel == "." || rel == "" {
		return 0, nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	return len(parts), nil
}

func autoReplyRoots(
	stateRoot string,
	fromID string,
	sessionID string,
) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 3)
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

	appendRoot(sessionUploadsRoot(stateRoot, fromID, sessionID))
	if cwd, err := os.Getwd(); err == nil {
		appendRoot(cwd)
	}
	appendRoot(stateRoot)
	return out
}

func sessionUploadsRoot(
	stateRoot string,
	fromID string,
	sessionID string,
) string {
	if strings.TrimSpace(stateRoot) == "" ||
		strings.TrimSpace(fromID) == "" ||
		strings.TrimSpace(sessionID) == "" {
		return ""
	}
	store, err := uploads.NewStore(stateRoot)
	if err != nil {
		return ""
	}
	return store.ScopeDir(uploads.Scope{
		Channel:   channelID,
		UserID:    fromID,
		SessionID: sessionID,
	})
}

func cleanReplyFilePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return trimmed
	}
	return filepath.Clean(trimmed)
}

func matchesReplyFileName(found string, want string) bool {
	return found == want || strings.HasSuffix(found, "-"+want)
}

func pathUnderAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		if pathUnderRoot(path, root) {
			return true
		}
	}
	return false
}
