//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package input

import (
	"strings"
	"unicode"
)

// unquoteGitPath decodes a Git pathname that may be double-quoted with
// C-style escapes (as produced by git for spaces and special characters).
func unquoteGitPath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if s[0] != '"' {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 1
	for i < len(s) {
		if s[i] == '"' {
			return b.String()
		}
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case '"', '\\':
			b.WriteByte(s[i])
		case 'a':
			b.WriteByte('\a')
		case 'v':
			b.WriteByte('\v')
		default:
			// Octal escape \NNN (up to 3 digits) used by Git.
			if s[i] >= '0' && s[i] <= '7' {
				val := 0
				for n := 0; n < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7'; n++ {
					val = val*8 + int(s[i]-'0')
					i++
				}
				b.WriteByte(byte(val))
				continue
			}
			b.WriteByte(s[i])
		}
		i++
	}
	return b.String()
}

// splitGitPathTokens splits a Git path list respecting quoted pathnames.
func splitGitPathTokens(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	i := 0
	for i < len(s) {
		for i < len(s) && unicode.IsSpace(rune(s[i])) {
			i++
		}
		if i >= len(s) {
			break
		}
		if s[i] == '"' {
			end := i + 1
			for end < len(s) {
				if s[end] == '\\' && end+1 < len(s) {
					end += 2
					continue
				}
				if s[end] == '"' {
					end++
					break
				}
				end++
			}
			out = append(out, unquoteGitPath(s[i:end]))
			i = end
			continue
		}
		start := i
		for i < len(s) && !unicode.IsSpace(rune(s[i])) {
			i++
		}
		out = append(out, s[start:i])
	}
	return out
}

// parseDiffGitPath extracts the destination path from a diff --git header.
func parseDiffGitPath(line string) string {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "diff --git "))
	parts := splitGitPathTokens(rest)
	if len(parts) == 0 {
		return "unknown"
	}
	pick := parts[len(parts)-1]
	if len(parts) >= 2 {
		pick = parts[1]
	}
	pick = strings.TrimPrefix(pick, "b/")
	pick = strings.TrimPrefix(pick, "a/")
	if pick == "" || pick == "/dev/null" {
		if len(parts) >= 1 {
			alt := strings.TrimPrefix(strings.TrimPrefix(parts[0], "a/"), "b/")
			if alt != "" && alt != "/dev/null" {
				return alt
			}
		}
		return "unknown"
	}
	return pick
}

// parseDiffFileHeaderPath extracts a path from ---/+++ header lines.
func parseDiffFileHeaderPath(line string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"+++ ", "--- "} {
		if strings.HasPrefix(line, prefix) {
			line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
			break
		}
	}
	// Drop optional tab-separated timestamp metadata.
	if i := strings.IndexByte(line, '\t'); i >= 0 {
		line = line[:i]
	}
	path := unquoteGitPath(line)
	path = strings.TrimPrefix(path, "b/")
	path = strings.TrimPrefix(path, "a/")
	return path
}
