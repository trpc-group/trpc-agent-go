//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

// --- RES-001: Missing defer Close ---
//
// Detects os.Open, os.Create, os.OpenFile without a corresponding
// defer ...Close() within the next few lines.

var resourceOpenRe = compileRegexp(
	`(?:os\.Open|os\.Create|os\.OpenFile|os\.MkdirAll)\s*\(`,
)

type missingDeferCloseRule struct{}

func (r *missingDeferCloseRule) ID() string               { return "RES-001" }
func (r *missingDeferCloseRule) Category() types.Category { return types.CategoryResourceClose }

func (r *missingDeferCloseRule) Evaluate(fc *diff.FileChange) []types.Finding {
	var findings []types.Finding
	// Check both added and context lines for resource opens.
	lines := allLines(fc)
	added := addedLines(fc)
	for i, li := range lines {
		if !resourceOpenRe.MatchString(li.content) {
			continue
		}
		// Look ahead for defer ...Close() in the next 5 lines.
		hasClose := false
		end := i + 5
		if end > len(lines) {
			end = len(lines)
		}
		for j := i + 1; j < end; j++ {
			content := strings.TrimSpace(lines[j].content)
			// Skip comment lines — they may mention "defer Close"
			// without actually implementing it.
			if strings.HasPrefix(content, "//") {
				continue
			}
			if strings.Contains(content, "defer") &&
				strings.Contains(content, "Close()") {
				hasClose = true
				break
			}
		}
		// Also check if defer Close was removed (minus line).
		if !hasClose {
			for _, al := range added {
				_ = al
			}
			// Check if there's a removed defer Close.
			hasRemovedDefer := false
			for _, h := range fc.Hunks {
				for _, line := range h.Lines {
					if strings.HasPrefix(line, "-") &&
						strings.Contains(line, "defer") &&
						strings.Contains(line, "Close()") {
						hasRemovedDefer = true
						break
					}
				}
				if hasRemovedDefer {
					break
				}
			}
			if hasRemovedDefer {
				f := finding(r.ID(), types.SeverityHigh, r.Category(),
					fc, li.lineNum,
					"defer Close() was removed, resource may leak on error paths",
					strings.TrimSpace(li.content),
					"Restore defer f.Close() immediately after the open "+
						"call to ensure the resource is released on all "+
						"exit paths.",
				)
				f.Confidence = 0.85
				findings = append(findings, f)
			} else if isAddedLine(li, added) {
				f := finding(r.ID(), types.SeverityHigh, r.Category(),
					fc, li.lineNum,
					"Resource opened without defer Close()",
					strings.TrimSpace(li.content),
					"Add defer f.Close() immediately after the open call to "+
						"ensure the resource is released on all exit paths.",
				)
				f.Confidence = 0.75
				findings = append(findings, f)
			}
		}
	}
	return findings
}

// isAddedLine checks if the given line info corresponds to an added
// (not context) line.
func isAddedLine(target lineInfo, added []lineInfo) bool {
	for _, a := range added {
		if a.lineNum == target.lineNum && a.content == target.content {
			return true
		}
	}
	return false
}

// --- RES-002: Unclosed HTTP body ---
//
// Detects http.Get/http.Post or client.Do without defer
// resp.Body.Close().

var httpDoRe = compileRegexp(
	`(?:http\.(?:Get|Post|Head|Do|PostForm)|\.Do\s*\()`,
)

type unclosedHTTPBodyRule struct{}

func (r *unclosedHTTPBodyRule) ID() string               { return "RES-002" }
func (r *unclosedHTTPBodyRule) Category() types.Category { return types.CategoryResourceClose }

func (r *unclosedHTTPBodyRule) Evaluate(fc *diff.FileChange) []types.Finding {
	var findings []types.Finding
	lines := addedLines(fc)
	for i, li := range lines {
		if !httpDoRe.MatchString(li.content) {
			continue
		}
		hasClose := false
		end := i + 4
		if end > len(lines) {
			end = len(lines)
		}
		for j := i + 1; j < end; j++ {
			content := strings.TrimSpace(lines[j].content)
			if strings.HasPrefix(content, "//") {
				continue
			}
			if strings.Contains(content, "defer") &&
				strings.Contains(content, "Body.Close()") {
				hasClose = true
				break
			}
		}
		if !hasClose {
			f := finding(r.ID(), types.SeverityHigh, r.Category(),
				fc, li.lineNum,
				"HTTP response body may not be closed",
				strings.TrimSpace(li.content),
				"Add defer resp.Body.Close() after the HTTP call to avoid "+
					"connection leaks.",
			)
			f.Confidence = 0.7
			findings = append(findings, f)
		}
	}
	return findings
}
