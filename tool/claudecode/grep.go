//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package claudecode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newGrepTool(runtime *runtime) (tool.Tool, error) {
	return function.NewFunctionTool(
		func(ctx context.Context, in grepInput) (grepOutput, error) {
			baseDir := runtime.currentBaseDir()
			if out, handled, err := runLocalRipgrep(ctx, baseDir, in); handled {
				return out, err
			}
			return runFallbackGrep(runtime, baseDir, in)
		},
		function.WithName(toolGrep),
		function.WithDescription(grepDescription()),
	), nil
}

func grepDescription() string {
	return fmt.Sprintf(`Powerful repository search built on ripgrep when available, with a workspace-safe fallback.

Usage:
- ALWAYS use %s for repository search tasks. NEVER invoke grep or rg through %s for normal code search.
- Supports regular expressions.
- Use glob or type to narrow the file set.
- output_mode supports "content", "files_with_matches", and "count".
- Use multiline=true for patterns that must match across line boundaries.
- Use A, B, C, or context to request surrounding lines in content mode.
- Use head_limit and offset to page through large result sets.`, toolGrep, toolBash)
}

func runFallbackGrep(runtime *runtime, baseDir string, in grepInput) (grepOutput, error) {
	mode := strings.TrimSpace(in.OutputMode)
	if mode == "" {
		mode = "files_with_matches"
	}
	re, err := compileGrepPattern(in)
	if err != nil {
		return grepOutput{}, err
	}
	candidates, err := collectGrepCandidates(baseDir, in.Path, in.Glob, in.Type)
	if err != nil {
		return grepOutput{}, err
	}
	collector := newFallbackGrepCollector(mode)
	for _, absPath := range candidates {
		if err := collectFallbackGrepMatch(runtime, baseDir, absPath, re, in, collector); err != nil {
			return grepOutput{}, err
		}
	}
	return finalizeFallbackGrepOutput(baseDir, in, collector), nil
}

type fallbackGrepCollector struct {
	mode         string
	contentLines []string
	countLines   []string
	fileMatches  []string
}

func newFallbackGrepCollector(mode string) *fallbackGrepCollector {
	return &fallbackGrepCollector{
		mode:         mode,
		contentLines: make([]string, 0),
		countLines:   make([]string, 0),
		fileMatches:  make([]string, 0),
	}
}

func compileGrepPattern(in grepInput) (*regexp.Regexp, error) {
	pattern := in.Pattern
	if in.IgnoreCase != nil && *in.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	if in.Multiline {
		pattern = "(?s)" + pattern
	}
	return regexp.Compile(pattern)
}

func collectFallbackGrepMatch(
	runtime *runtime,
	baseDir string,
	absPath string,
	re *regexp.Regexp,
	in grepInput,
	collector *fallbackGrepCollector,
) error {
	snapshot, err := readLocalFileSnapshot(absPath, runtime.maxFileSize)
	if err != nil || isProbablyBinary(snapshot.Raw) {
		return nil
	}
	relPath := relativePath(baseDir, absPath)
	if in.Multiline {
		return collectFallbackMultilineMatch(snapshot.Content, relPath, re, in, collector)
	}
	return collectFallbackLineMatch(snapshot.Content, relPath, re, in, collector)
}

func collectFallbackMultilineMatch(
	content string,
	relPath string,
	re *regexp.Regexp,
	in grepInput,
	collector *fallbackGrepCollector,
) error {
	matches := re.FindAllStringIndex(content, -1)
	if len(matches) == 0 {
		return nil
	}
	lines := splitTextLines(content)
	switch collector.mode {
	case "count":
		collector.countLines = append(collector.countLines, fmt.Sprintf("%s:%d", relPath, len(matches)))
	case "content":
		matchedIndexes := multilineMatchLineIndexes(content, matches, len(lines))
		appendGrepContentLines(&collector.contentLines, relPath, lines, expandContextLines(matchedIndexes, len(lines), in), showGrepLineNumbers(in))
	default:
		collector.fileMatches = append(collector.fileMatches, relPath)
	}
	return nil
}

func collectFallbackLineMatch(
	content string,
	relPath string,
	re *regexp.Regexp,
	in grepInput,
	collector *fallbackGrepCollector,
) error {
	lines := splitTextLines(content)
	matchedIndexes := make([]int, 0)
	for idx, line := range lines {
		if re.MatchString(line) {
			matchedIndexes = append(matchedIndexes, idx)
		}
	}
	if len(matchedIndexes) == 0 {
		return nil
	}
	switch collector.mode {
	case "count":
		collector.countLines = append(collector.countLines, fmt.Sprintf("%s:%d", relPath, len(matchedIndexes)))
	case "content":
		appendGrepContentLines(&collector.contentLines, relPath, lines, expandContextLines(matchedIndexes, len(lines), in), showGrepLineNumbers(in))
	default:
		collector.fileMatches = append(collector.fileMatches, relPath)
	}
	return nil
}

func appendGrepContentLines(out *[]string, relPath string, lines []string, indexes []int, showLineNumbers bool) {
	for _, idx := range indexes {
		if showLineNumbers {
			*out = append(*out, fmt.Sprintf("%s:%d:%s", relPath, idx+1, lines[idx]))
			continue
		}
		*out = append(*out, fmt.Sprintf("%s:%s", relPath, lines[idx]))
	}
}

func showGrepLineNumbers(in grepInput) bool {
	if in.ShowLineNum != nil {
		return *in.ShowLineNum
	}
	return true
}

func finalizeFallbackGrepOutput(baseDir string, in grepInput, collector *fallbackGrepCollector) grepOutput {
	offset := grepOffset(in)
	limit := grepLimit(in)
	switch collector.mode {
	case "count":
		return finalizeFallbackCountOutput(offset, limit, collector.countLines)
	case "content":
		sliced, appliedLimit := sliceStrings(collector.contentLines, offset, limit)
		return grepOutput{
			Mode:          "content",
			NumFiles:      0,
			Filenames:     []string{},
			Content:       strings.Join(sliced, "\n"),
			NumLines:      len(sliced),
			AppliedLimit:  appliedLimit,
			AppliedOffset: offset,
		}
	default:
		sortedFiles := sortGrepPathsByMtime(baseDir, collector.fileMatches)
		sliced, appliedLimit := sliceStrings(sortedFiles, offset, limit)
		return grepOutput{
			Mode:          "files_with_matches",
			NumFiles:      len(sliced),
			Filenames:     sliced,
			AppliedLimit:  appliedLimit,
			AppliedOffset: offset,
		}
	}
}

func finalizeFallbackCountOutput(offset int, limit int, countLines []string) grepOutput {
	sortedCounts := sortedCopy(countLines)
	sliced, appliedLimit := sliceStrings(sortedCounts, offset, limit)
	totalMatches := 0
	for _, line := range sliced {
		_, count, ok := parseGrepCountLine(line)
		if ok {
			totalMatches += count
		}
	}
	return grepOutput{
		Mode:          "count",
		NumFiles:      len(sliced),
		Content:       strings.Join(sliced, "\n"),
		NumMatches:    totalMatches,
		AppliedLimit:  appliedLimit,
		AppliedOffset: offset,
	}
}

func multilineMatchLineIndexes(content string, matches [][]int, totalLines int) []int {
	if len(matches) == 0 || totalLines <= 0 {
		return nil
	}
	lineStarts := make([]int, 1, totalLines)
	lineStarts[0] = 0
	for idx, r := range content {
		if r != '\n' || len(lineStarts) == totalLines {
			continue
		}
		lineStarts = append(lineStarts, idx+1)
	}
	seen := make(map[int]struct{}, len(matches))
	indexes := make([]int, 0, len(matches)*2)
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		startLine := lineIndexForOffset(lineStarts, match[0])
		endOffset := match[1] - 1
		if endOffset < match[0] {
			endOffset = match[0]
		}
		endLine := lineIndexForOffset(lineStarts, endOffset)
		for line := startLine; line <= endLine; line++ {
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			indexes = append(indexes, line)
		}
	}
	slices.Sort(indexes)
	return indexes
}

func lineIndexForOffset(lineStarts []int, offset int) int {
	if len(lineStarts) == 0 || offset <= 0 {
		return 0
	}
	lineIndex := 0
	for idx := 1; idx < len(lineStarts); idx++ {
		if lineStarts[idx] > offset {
			break
		}
		lineIndex = idx
	}
	return lineIndex
}

func collectGrepCandidates(baseDir string, pathValue string, globValue string, typeValue string) ([]string, error) {
	searchRoot := baseDir
	if strings.TrimSpace(pathValue) != "" {
		_, absPath, err := normalizePath(baseDir, pathValue)
		if err != nil {
			return nil, err
		}
		searchRoot = absPath
	}
	info, err := os.Stat(searchRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{searchRoot}, nil
	}
	globPatterns := splitGlobPatterns(globValue)
	if len(globPatterns) == 0 {
		globPatterns = []string{"**/*"}
	}
	typePatterns := typePatterns(typeValue)
	candidates := make([]string, 0, 64)
	err = filepath.WalkDir(searchRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			for _, excluded := range grepExcludedDirs {
				if d.Name() == excluded {
					return filepath.SkipDir
				}
			}
			return nil
		}
		relToRoot, err := filepath.Rel(searchRoot, path)
		if err != nil {
			return err
		}
		relToRoot = filepath.ToSlash(relToRoot)
		if !matchesAnyPattern(relToRoot, globPatterns) {
			return nil
		}
		if len(typePatterns) > 0 && !matchesAnyPattern(relToRoot, typePatterns) {
			return nil
		}
		candidates = append(candidates, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(candidates)
	return candidates, nil
}

func matchesAnyPattern(value string, patterns []string) bool {
	for _, pattern := range patterns {
		ok, err := doublestar.PathMatch(pattern, value)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func expandContextLines(matches []int, total int, in grepInput) []int {
	before := 0
	after := 0
	switch {
	case in.ContextAlt != nil:
		before = *in.ContextAlt
		after = *in.ContextAlt
	case in.Context != nil:
		before = *in.Context
		after = *in.Context
	default:
		if in.Before != nil {
			before = *in.Before
		}
		if in.After != nil {
			after = *in.After
		}
	}
	seen := make(map[int]struct{}, len(matches))
	indexes := make([]int, 0, len(matches))
	for _, idx := range matches {
		start := idx - before
		if start < 0 {
			start = 0
		}
		end := idx + after
		if end >= total {
			end = total - 1
		}
		for line := start; line <= end; line++ {
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			indexes = append(indexes, line)
		}
	}
	slices.Sort(indexes)
	return indexes
}

func typePatterns(typeValue string) []string {
	switch strings.TrimSpace(strings.ToLower(typeValue)) {
	case "":
		return nil
	case "go":
		return []string{"**/*.go"}
	case "js":
		return []string{"**/*.js", "**/*.cjs", "**/*.mjs"}
	case "ts":
		return []string{"**/*.ts", "**/*.tsx", "**/*.mts", "**/*.cts"}
	case "py":
		return []string{"**/*.py"}
	case "java":
		return []string{"**/*.java"}
	case "rust", "rs":
		return []string{"**/*.rs"}
	case "json":
		return []string{"**/*.json"}
	case "md", "markdown":
		return []string{"**/*.md"}
	case "yaml", "yml":
		return []string{"**/*.yaml", "**/*.yml"}
	case "txt":
		return []string{"**/*.txt"}
	default:
		return []string{"**/*." + strings.TrimPrefix(strings.ToLower(typeValue), ".")}
	}
}

func sliceStrings(items []string, offset int, limit int) ([]string, *int) {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []string{}, nil
	}
	remaining := items[offset:]
	if limit == 0 {
		return remaining, nil
	}
	if limit < 0 || limit >= len(remaining) {
		return remaining, nil
	}
	appliedLimit := limit
	return remaining[:limit], &appliedLimit
}

func runLocalRipgrep(
	ctx context.Context,
	baseDir string,
	in grepInput,
) (grepOutput, bool, error) {
	if ripgrepCommand() == "" {
		return grepOutput{}, false, nil
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return grepOutput{}, true, err
	}
	targetPath := "."
	if strings.TrimSpace(in.Path) != "" {
		relPath, _, err := normalizePath(baseDir, in.Path)
		if err != nil {
			return grepOutput{}, true, err
		}
		if relPath != "" {
			targetPath = relPath
		}
	}
	return runRipgrepCommand(ctx, baseAbs, targetPath, in)
}

func runRipgrepCommand(
	ctx context.Context,
	baseAbs string,
	targetPath string,
	in grepInput,
) (grepOutput, bool, error) {
	mode := strings.TrimSpace(in.OutputMode)
	if mode == "" {
		mode = "files_with_matches"
	}
	lines, err := execRipgrep(ctx, baseAbs, buildRipgrepArgs(mode, targetPath, in)...)
	if err != nil {
		return grepOutput{}, true, err
	}
	return formatRipgrepOutput(baseAbs, mode, in, lines), true, nil
}

func buildRipgrepArgs(mode string, targetPath string, in grepInput) []string {
	args := []string{"--hidden", "--max-columns", "500"}
	args = appendRipgrepExcludes(args)
	args = appendRipgrepMode(args, mode, in)
	args = appendRipgrepPattern(args, in.Pattern)
	if strings.TrimSpace(in.Type) != "" {
		args = append(args, "--type", strings.TrimSpace(in.Type))
	}
	for _, pattern := range splitGlobPatterns(in.Glob) {
		args = append(args, "--glob", pattern)
	}
	return append(args, targetPath)
}

func appendRipgrepExcludes(args []string) []string {
	for _, dir := range grepExcludedDirs {
		args = append(args, "--glob", "!"+dir)
	}
	return args
}

func appendRipgrepMode(args []string, mode string, in grepInput) []string {
	if in.Multiline {
		args = append(args, "-U", "--multiline-dotall")
	}
	if in.IgnoreCase != nil && *in.IgnoreCase {
		args = append(args, "-i")
	}
	switch mode {
	case "files_with_matches":
		args = append(args, "-l")
	case "count":
		args = append(args, "-c")
	case "content":
		if showGrepLineNumbers(in) {
			args = append(args, "-n")
		}
		args = appendRipgrepContext(args, in)
	}
	return args
}

func appendRipgrepContext(args []string, in grepInput) []string {
	switch {
	case in.ContextAlt != nil:
		return append(args, "-C", strconv.Itoa(*in.ContextAlt))
	case in.Context != nil:
		return append(args, "-C", strconv.Itoa(*in.Context))
	default:
		if in.Before != nil {
			args = append(args, "-B", strconv.Itoa(*in.Before))
		}
		if in.After != nil {
			args = append(args, "-A", strconv.Itoa(*in.After))
		}
		return args
	}
}

func appendRipgrepPattern(args []string, pattern string) []string {
	if strings.HasPrefix(pattern, "-") {
		return append(args, "-e", pattern)
	}
	return append(args, pattern)
}

func formatRipgrepOutput(baseAbs string, mode string, in grepInput, lines []string) grepOutput {
	offset := grepOffset(in)
	limit := grepLimit(in)
	switch mode {
	case "content":
		sliced, appliedLimit := sliceStrings(lines, offset, limit)
		return grepOutput{
			Mode:          "content",
			Content:       strings.Join(sliced, "\n"),
			NumLines:      len(sliced),
			AppliedLimit:  appliedLimit,
			AppliedOffset: offset,
		}
	case "count":
		return formatRipgrepCountOutput(offset, limit, lines)
	default:
		sorted := sortGrepPathsByMtime(baseAbs, lines)
		sliced, appliedLimit := sliceStrings(sorted, offset, limit)
		return grepOutput{
			Mode:          "files_with_matches",
			NumFiles:      len(sliced),
			Filenames:     sliced,
			AppliedLimit:  appliedLimit,
			AppliedOffset: offset,
		}
	}
}

func formatRipgrepCountOutput(offset int, limit int, lines []string) grepOutput {
	formatted := make([]string, 0, len(lines))
	for _, line := range lines {
		path, count, ok := parseGrepCountLine(line)
		if ok {
			formatted = append(formatted, fmt.Sprintf("%s:%d", path, count))
			continue
		}
		formatted = append(formatted, line)
	}
	sliced, appliedLimit := sliceStrings(formatted, offset, limit)
	totalMatches := 0
	for _, line := range sliced {
		_, count, ok := parseGrepCountLine(line)
		if ok {
			totalMatches += count
		}
	}
	return grepOutput{
		Mode:          "count",
		NumFiles:      len(sliced),
		Content:       strings.Join(sliced, "\n"),
		NumMatches:    totalMatches,
		AppliedLimit:  appliedLimit,
		AppliedOffset: offset,
	}
}

func grepOffset(in grepInput) int {
	if in.Offset != nil && *in.Offset > 0 {
		return *in.Offset
	}
	return 0
}

func grepLimit(in grepInput) int {
	if in.HeadLimit != nil {
		return *in.HeadLimit
	}
	return defaultGrepHeadLimit
}

func execRipgrep(
	ctx context.Context,
	baseAbs string,
	args ...string,
) ([]string, error) {
	result, err := runCapturedProcess(ctx, baseAbs, nil, ripgrepCommand(), args...)
	if err != nil || result.ExitCode != 0 {
		if result.ExitCode == 1 {
			return []string{}, nil
		}
		msg := strings.TrimSpace(string(result.Stderr))
		if msg == "" {
			if err != nil {
				msg = err.Error()
			} else {
				msg = fmt.Sprintf("ripgrep exited with code %d", result.ExitCode)
			}
		}
		return nil, fmt.Errorf("ripgrep failed: %s", msg)
	}
	return splitRipgrepLines(string(result.Stdout)), nil
}

func splitRipgrepLines(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		line = strings.TrimPrefix(line, "./")
		line = strings.TrimPrefix(line, ".\\")
		out = append(out, filepath.ToSlash(line))
	}
	return out
}

func splitGlobPatterns(raw string) []string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return nil
	}
	rawPatterns := strings.Fields(clean)
	out := make([]string, 0, len(rawPatterns))
	for _, rawPattern := range rawPatterns {
		if strings.Contains(rawPattern, "{") && strings.Contains(rawPattern, "}") {
			out = append(out, rawPattern)
			continue
		}
		for _, part := range strings.Split(rawPattern, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			out = append(out, part)
		}
	}
	return out
}

func sortGrepPathsByMtime(baseAbs string, paths []string) []string {
	type fileEntry struct {
		path  string
		mtime int64
	}
	entries := make([]fileEntry, 0, len(paths))
	for _, path := range paths {
		mtime := int64(0)
		if info, err := os.Stat(filepath.Join(baseAbs, filepath.FromSlash(path))); err == nil {
			mtime = info.ModTime().UnixMilli()
		}
		entries = append(entries, fileEntry{path: filepath.ToSlash(path), mtime: mtime})
	}
	slices.SortFunc(entries, func(a, b fileEntry) int {
		if a.mtime == b.mtime {
			return strings.Compare(a.path, b.path)
		}
		if a.mtime > b.mtime {
			return -1
		}
		return 1
	})
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.path)
	}
	return out
}

func parseGrepCountLine(line string) (string, int, bool) {
	idx := strings.LastIndex(line, ":")
	if idx <= 0 || idx >= len(line)-1 {
		return "", 0, false
	}
	count, err := strconv.Atoi(strings.TrimSpace(line[idx+1:]))
	if err != nil {
		return "", 0, false
	}
	return line[:idx], count, true
}

func ripgrepCommand() string {
	ripgrepOnce.Do(func() {
		path, err := ripgrepLookPath("rg")
		if err == nil {
			ripgrepPath = path
		}
	})
	return ripgrepPath
}
