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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var hunkHeaderRE = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)
var packageDeclRE = regexp.MustCompile(`^\s*package\s+([A-Za-z_][A-Za-z0-9_]*)\b`)

func ParseUnifiedDiff(raw string) (ParsedDiff, error) {
	sum := sha256.Sum256([]byte(raw))
	pd := ParsedDiff{
		RawHash: hex.EncodeToString(sum[:]),
		Raw:     raw,
	}
	var currentFile DiffFile
	var currentHunk *DiffHunk
	oldLine, newLine := 0, 0
	seenFiles := map[string]bool{}

	flushHunk := func() {
		if currentHunk != nil {
			pd.Hunks = append(pd.Hunks, *currentHunk)
			currentHunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if currentFile.NewPath != "" && !seenFiles[currentFile.NewPath] {
			currentFile.IsGo = strings.HasSuffix(currentFile.NewPath, ".go")
			currentFile.IsTest = strings.HasSuffix(currentFile.NewPath, "_test.go")
			pd.Files = append(pd.Files, currentFile)
			seenFiles[currentFile.NewPath] = true
		}
	}

	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			currentFile = DiffFile{}
			oldPath, newPath, ok := parseDiffGitPaths(line)
			if ok {
				currentFile.OldPath = cleanDiffPath(oldPath)
				currentFile.NewPath = cleanDiffPath(newPath)
			}
		case strings.HasPrefix(line, "@@ "):
			flushHunk()
			m := hunkHeaderRE.FindStringSubmatch(line)
			if m == nil {
				return ParsedDiff{}, fmt.Errorf("invalid hunk header: %s", line)
			}
			oldStart := atoiDefault(m[1], 0)
			oldCount := atoiDefault(m[2], 1)
			newStart := atoiDefault(m[3], 0)
			newCount := atoiDefault(m[4], 1)
			oldLine = oldStart
			newLine = newStart
			currentHunk = &DiffHunk{
				File:     currentFile.NewPath,
				OldStart: oldStart,
				OldCount: oldCount,
				NewStart: newStart,
				NewCount: newCount,
			}
		case currentHunk != nil:
			if line == `\ No newline at end of file` {
				continue
			}
			kind := byte(' ')
			text := line
			if line != "" {
				kind = line[0]
				text = line[1:]
			}
			dl := DiffLine{Kind: kind, Text: text}
			switch kind {
			case '+':
				dl.NewLine = newLine
				newLine++
				pd.Summary.AddedLines++
			case '-':
				dl.OldLine = oldLine
				oldLine++
				pd.Summary.DeletedLines++
			default:
				dl.OldLine = oldLine
				dl.NewLine = newLine
				oldLine++
				newLine++
			}
			currentHunk.Lines = append(currentHunk.Lines, dl)
		case strings.HasPrefix(line, "--- "):
			currentFile.OldPath = cleanDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "--- ")))
		case strings.HasPrefix(line, "+++ "):
			currentFile.NewPath = cleanDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
		}
	}
	flushFile()
	pd.Summary.FilesChanged = len(pd.Files)
	for _, f := range pd.Files {
		if f.IsGo {
			pd.Summary.GoFiles++
		}
	}
	attachPackageInfo(&pd)
	return pd, nil
}

func cleanDiffPath(path string) string {
	path = strings.TrimSpace(path)
	path = decodeGitQuotedPath(path)
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	path = decodeGitQuotedPath(path)
	if path == "/dev/null" {
		return ""
	}
	return filepathSlash(path)
}

func parseDiffGitPaths(line string) (string, string, bool) {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "diff --git "))
	oldPath, rest, ok := consumeGitPathToken(rest)
	if !ok {
		return "", "", false
	}
	newPath, _, ok := consumeGitPathToken(rest)
	if !ok {
		return "", "", false
	}
	return oldPath, newPath, true
}

func consumeGitPathToken(raw string) (string, string, bool) {
	raw = strings.TrimLeft(raw, " \t")
	if raw == "" {
		return "", "", false
	}
	if raw[0] != '"' {
		idx := strings.IndexAny(raw, " \t")
		if idx < 0 {
			return raw, "", true
		}
		return raw[:idx], raw[idx+1:], true
	}
	escaped := false
	for i := 1; i < len(raw); i++ {
		switch {
		case escaped:
			escaped = false
		case raw[i] == '\\':
			escaped = true
		case raw[i] == '"':
			return raw[:i+1], raw[i+1:], true
		}
	}
	return "", "", false
}

func decodeGitQuotedPath(path string) string {
	path = strings.TrimSpace(path)
	if len(path) < 2 || path[0] != '"' || path[len(path)-1] != '"' {
		return path
	}
	decoded, err := strconv.Unquote(path)
	if err != nil {
		return path
	}
	return decoded
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func attachPackageInfo(pd *ParsedDiff) {
	packageByFile := packageNamesByFile(pd.Hunks)
	for i := range pd.Files {
		file := &pd.Files[i]
		if !file.IsGo {
			continue
		}
		file.PackagePath = packagePath(file.NewPath)
		if packageByFile[file.NewPath] != "" {
			file.PackageName = packageByFile[file.NewPath]
		}
	}
	attachPackageInfoFromFiles(pd)
}

func attachPackageInfoFromFiles(pd *ParsedDiff) {
	pd.Packages = nil
	packages := map[string]*GoPackageInfo{}
	for i := range pd.Files {
		file := &pd.Files[i]
		if !file.IsGo {
			continue
		}
		if file.PackagePath == "" {
			file.PackagePath = packagePath(file.NewPath)
		}
		if file.PackageName == "" && file.PackagePath != "." {
			file.PackageName = path.Base(file.PackagePath)
		}
		info := packages[file.PackagePath]
		if info == nil {
			info = &GoPackageInfo{PackagePath: file.PackagePath}
			packages[file.PackagePath] = info
		}
		if info.PackageName == "" {
			info.PackageName = file.PackageName
		}
		info.Files = append(info.Files, file.NewPath)
	}
	for _, key := range sortedPackagePaths(packages) {
		pd.Packages = append(pd.Packages, *packages[key])
	}
}

func packageNamesByFile(hunks []DiffHunk) map[string]string {
	out := map[string]string{}
	for _, h := range hunks {
		if out[h.File] != "" {
			continue
		}
		for _, line := range h.Lines {
			if line.Kind == '-' {
				continue
			}
			if m := packageDeclRE.FindStringSubmatch(line.Text); m != nil {
				out[h.File] = m[1]
				break
			}
		}
	}
	return out
}

func packagePath(file string) string {
	dir := path.Dir(filepathSlash(file))
	if dir == "." {
		return "."
	}
	return dir
}

func filepathSlash(file string) string {
	return strings.TrimPrefix(strings.ReplaceAll(file, "\\", "/"), "./")
}

func sortedPackagePaths(packages map[string]*GoPackageInfo) []string {
	keys := make([]string, 0, len(packages))
	for key := range packages {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
