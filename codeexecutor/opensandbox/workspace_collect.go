//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package opensandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// Collect returns files in the workspace that match the supplied
// globs. Files are read back through the SDK's DownloadFile API and
// sized against maxReadSizeBytes.
func (r *workspaceRuntime) Collect(
	ctx context.Context,
	ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	patterns = codeexecutor.NormalizeGlobs(patterns)
	if len(patterns) == 0 {
		return nil, nil
	}
	if err := r.validateWorkspace(ws); err != nil {
		return nil, err
	}
	sb, err := r.sandbox()
	if err != nil {
		return nil, err
	}

	paths, err := r.listFilesByGlob(ctx, ws.Path, patterns)
	if err != nil {
		return nil, err
	}

	// Resolve symlinks for all collected paths in a single round-trip
	// to prevent a symlink inside the workspace from causing Collect
	// to read files outside the workspace. A path that resolves
	// outside ws.Path is skipped.
	resolvedPaths, err := r.resolveSandboxPaths(ctx, paths, ws.Path)
	if err != nil {
		return nil, err
	}

	// Pre-allocate at most maxCollectFiles slots: resolvedPaths may
	// contain thousands of entries (before the loop below truncates),
	// and pre-allocating based on the untruncated length wastes memory.
	prealloc := len(resolvedPaths)
	if prealloc > maxCollectFiles {
		prealloc = maxCollectFiles
	}
	out := make([]codeexecutor.File, 0, prealloc)
	seen := map[string]bool{}
	var totalBytes int64
	// limitsHit only when an eligible file is actually skipped (or the
	// server-side listing was capped). Filling the budget exactly with
	// the last match is complete collection, not partial.
	limitsHit := false
	listingCapped := len(paths) > maxCollectFiles
	for _, fr := range resolvedPaths {
		rel := strings.TrimPrefix(fr.path, ws.Path+"/")
		if rel == fr.path {
			rel = filepath.ToSlash(fr.path)
		}
		if codeexecutor.IsRootMetadataTempPath(rel) {
			continue
		}
		if seen[rel] {
			continue
		}
		// Eligible file — apply budgets only after metadata/dedup filters.
		if len(out) >= maxCollectFiles {
			limitsHit = true
			break
		}
		remaining := maxCollectTotalBytes - totalBytes
		if remaining <= 0 {
			limitsHit = true
			break
		}
		seen[rel] = true
		if remaining > maxReadSizeBytes {
			remaining = maxReadSizeBytes
		}
		data, size, truncated, err := r.readFile(ctx, sb, fr.path, remaining, fr.size)
		if err != nil {
			return nil, err
		}
		totalBytes += int64(len(data))
		mime := http.DetectContentType(data)
		out = append(out, codeexecutor.File{
			Name:      rel,
			Content:   string(data),
			MIMEType:  mime,
			SizeBytes: size,
			Truncated: truncated,
		})
	}
	// Server-side listing stopped early: more matches may exist beyond
	// what we enumerated (even if we collected every listed eligible file).
	if listingCapped {
		limitsHit = true
	}
	if limitsHit {
		out = append(out, codeexecutor.File{
			Name:     collectLimitsHitMarkerName,
			Content:  collectLimitsHitMarkerContent(maxCollectFiles, maxCollectTotalBytes),
			MIMEType: "text/plain",
		})
	}
	return out, nil
}

// collectLimitsHitMarkerName is a synthetic entry appended when Collect
// stops early due to aggregate file-count or total-byte budgets. Callers
// that only inspect per-file Truncated would otherwise treat a partial
// result as complete.
const collectLimitsHitMarkerName = ".opensandbox_collect_limits_hit"

func collectLimitsHitMarkerContent(maxFiles int, maxBytes int64) string {
	return fmt.Sprintf(
		"collect stopped early: aggregate limits reached "+
			"(max files=%d, max total bytes=%d); result is partial",
		maxFiles, maxBytes,
	)
}

// resolveSandboxPaths resolves the real paths of multiple targets
// inside the sandbox in a single bash invocation, then filters out
// any that resolve outside wsBase. This is the batch version of
// resolveSandboxPath, used by Collect to avoid one round-trip per
// search result.
func (r *workspaceRuntime) resolveSandboxPaths(
	ctx context.Context, results []fileSearchResult, wsBase string,
) ([]fileSearchResult, error) {
	if len(results) == 0 {
		return results, nil
	}
	// Use printf with a NUL-separated format to avoid ambiguity from
	// readlink's own newline output. Each result is on exactly one
	// line, with no extra echo that would create blank lines.
	var script strings.Builder
	script.WriteString("for p in")
	for _, fr := range results {
		script.WriteByte(' ')
		script.WriteString(shellQuote(fr.path))
	}
	script.WriteString(`; do r=$(readlink -f -- "$p" 2>/dev/null) || r=""; printf '%s\n' "$r"; done`)
	out, err := r.runBash(ctx, script.String(), defaultCollectTimeout)
	if err != nil {
		return nil, fmt.Errorf(
			"opensandbox: resolve paths: %w", err,
		)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != len(results) {
		// Fallback: if the batch script returned unexpected output,
		// resolve each path individually.
		filtered := make([]fileSearchResult, 0, len(results))
		for _, fr := range results {
			resolved, err := r.resolveSandboxPath(ctx, fr.path, wsBase)
			if err != nil {
				continue // skip paths that escape
			}
			filtered = append(filtered, fileSearchResult{
				path: resolved, size: fr.size,
			})
		}
		return filtered, nil
	}
	filtered := make([]fileSearchResult, 0, len(results))
	for i, line := range lines {
		resolved := strings.TrimSpace(line)
		if resolved == "" || !pathUnder(resolved, wsBase) {
			continue
		}
		filtered = append(filtered, fileSearchResult{
			path: resolved, size: results[i].size,
		})
	}
	return filtered, nil
}

// StageInputs maps external inputs into the sandbox workspace.
//
// Not implemented in v1; returns ErrNotImplementedV1.
func (r *workspaceRuntime) StageInputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	_ = ctx
	_ = ws
	_ = specs
	return errNotImplementedV1
}

// CollectOutputs applies the declarative output spec in the sandbox.
//
// Not implemented in v1; returns ErrNotImplementedV1.
func (r *workspaceRuntime) CollectOutputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	_ = ctx
	_ = ws
	_ = spec
	return codeexecutor.OutputManifest{}, errNotImplementedV1
}

// readFile reads up to limit bytes from a remote path via the SDK's
// DownloadFile API. Returns the data, the file's full size (which
// may exceed len(data) when truncated), and a truncated flag. knownSize
// is the real file size from SearchFiles metadata; when positive it is
// used as the returned size so callers get an accurate SizeBytes even
// for files larger than limit. When knownSize is non-positive, the size
// falls back to the number of bytes actually read (capped at limit+1).
//
// The returned size is always at least len(data) to prevent stale
// metadata from making SizeBytes smaller than the actual content read.
//
// Truncated is true only when the read actually hit the limit+1 cap
// (proving the file is at least limit+1 bytes). This avoids false
// positives when the file shrank between SearchFiles and DownloadFile:
// in that case readBytes < limit, so the read reached EOF and the file
// was not truncated — even though stale knownSize may exceed len(data).
func (r *workspaceRuntime) readFile(
	ctx context.Context, sb *osb.Sandbox, full string, limit int64,
	knownSize int64,
) ([]byte, int64, bool, error) {
	if limit <= 0 {
		limit = maxReadSizeBytes
	}
	// Request one extra byte to detect truncation: if the server
	// returns limit+1 bytes, the file exceeds the cap.
	rangeHeader := fmt.Sprintf("bytes=0-%d", limit)
	rc, err := sb.DownloadFile(ctx, full, rangeHeader)
	if err != nil {
		return nil, 0, false, err
	}
	defer rc.Close()
	// Cap the read at limit+1 bytes regardless of whether the server
	// honors the Range header. Without this, a server/proxy that
	// ignores Range would stream the entire file into memory before
	// the truncation check below fires.
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, 0, false, err
	}
	readBytes := int64(len(data))
	// Truncated iff we read the full limit+1 bytes, proving the file
	// is at least limit+1 bytes long. This is the only reliable signal:
	// comparing knownSize to len(data) produces false positives when
	// the file shrank between SearchFiles and DownloadFile.
	truncated := readBytes > limit
	if truncated {
		data = data[:limit]
	}
	// Prefer the real size from SearchFiles metadata; fall back to
	// the byte count we actually read when metadata is unavailable.
	// Use max(knownSize, readBytes) — readBytes (before truncation)
	// so hitting the limit+1 detection is reflected in size even when
	// knownSize is stale (file grew between SearchFiles and Download).
	size := knownSize
	if size < readBytes {
		size = readBytes
	}
	return data, size, truncated, nil
}

// fileSearchResult carries a file path and its real size as reported
// by the sandbox shell. The size is used by Collect to set
// File.SizeBytes accurately even when the file content is truncated
// by readFile's byte cap.
type fileSearchResult struct {
	path string
	size int64
}

// listFilesByGlob resolves the provided patterns inside the sandbox
// using a bash globstar script and returns absolute file paths with
// their real sizes. This replaces the SDK's SearchFiles API, which
// returns the complete []FileInfo in a single HTTP response — when
// model-generated code creates tens of thousands of matching files,
// the SDK decodes the full JSON array into memory before the caller
// can apply any cap, causing unbounded host memory consumption.
//
// The bash script caps output at maxCollectFiles+1 lines on the
// server side (via a counter that breaks the loop), so only bounded
// data traverses the network and enters host memory. The +1 margin
// lets Collect detect that the cap was reached.
//
// Patterns without a path separator get a **/ prefix so they match
// recursively across the full directory tree, preserving the previous
// SearchFiles behaviour of glob.PathMatch(pattern, info.Name()).
func (r *workspaceRuntime) listFilesByGlob(
	ctx context.Context, wsPath string, patterns []string,
) ([]fileSearchResult, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	// Build a bash script that:
	// 1. cd into wsPath and resolve the real base path
	// 2. Enable globstar + nullglob + dotglob for recursive matching
	// 3. For each pattern, prepend **/ if it has no path separator
	// 4. For each match, resolve symlinks and verify the real path
	//    stays under the workspace base
	// 5. Dedup by resolved path BEFORE counting (overlapping patterns
	//    such as **/* and **/*.txt must not double-spend the budget)
	// 6. Print "path\tsize" for each unique valid match
	// 7. Stop after maxCollectFiles+1 unique results (server-side cap)
	var cmd strings.Builder
	cmd.WriteString("cd ")
	cmd.WriteString(shellQuote(wsPath))
	cmd.WriteString(" && __osb_base=$(readlink -f . 2>/dev/null || pwd); ")
	cmd.WriteString("printf '__OSB_BASE__=%s\\n' \"$__osb_base\"; ")
	cmd.WriteString("shopt -s globstar nullglob dotglob; ")
	// Associative set of already-emitted resolved paths so overlapping
	// patterns only consume one unit of the unique-file budget.
	cmd.WriteString("declare -A __osb_seen=(); ")
	cmd.WriteString("__osb_count=0; __osb_cap=")
	cmd.WriteString(strconv.Itoa(maxCollectFiles + 1))
	cmd.WriteString("; for p in")
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		cmd.WriteByte(' ')
		cmd.WriteString(shellQuote(p))
	}
	cmd.WriteString("; do")
	// Prepend **/ for patterns without a path separator so they match
	// recursively (matching SearchFiles' glob.PathMatch on Name()).
	// Note: in bash case patterns, ** is NOT globstar — it behaves as
	// *, so we must NOT include |** in the pattern or it would match
	// every non-empty string and make the *) branch dead code.
	cmd.WriteString(" case \"$p\" in */*) ;; *) p=\"**/$p\";; esac; ")
	cmd.WriteString("for f in $p; do")
	cmd.WriteString(" if [ \"$__osb_count\" -ge \"$__osb_cap\" ]; then break 2; fi; ")
	cmd.WriteString("if [ -f \"$f\" ]; then ")
	cmd.WriteString("__osb_rp=$(readlink -f \"$f\" 2>/dev/null || echo \"$(pwd)/$f\"); ")
	cmd.WriteString("case \"$__osb_rp\" in ")
	cmd.WriteString("\"$__osb_base\"/*|\"$__osb_base\") ")
	// Skip paths already counted under an earlier overlapping pattern.
	cmd.WriteString("if [ -n \"${__osb_seen[$__osb_rp]+x}\" ]; then continue; fi; ")
	cmd.WriteString("__osb_seen[$__osb_rp]=1; ")
	// stat -c %s is GNU stat (available in the code-interpreter image);
	// fall back to 0 if stat fails.
	cmd.WriteString("__osb_size=$(stat -c %s \"$__osb_rp\" 2>/dev/null || echo 0); ")
	cmd.WriteString("printf '%s\\t%s\\n' \"$__osb_rp\" \"$__osb_size\"; ")
	cmd.WriteString("__osb_count=$((__osb_count + 1)); ")
	cmd.WriteString(";; ")
	cmd.WriteString("esac; ")
	cmd.WriteString("fi; ")
	cmd.WriteString("done; ")
	cmd.WriteString("done")

	stdout, err := r.runBash(ctx, cmd.String(), defaultCollectTimeout)
	if err != nil {
		return nil, fmt.Errorf("opensandbox: list files by glob: %w", err)
	}

	var out []fileSearchResult
	seen := map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip the __OSB_BASE__ marker line emitted by the script.
		if strings.HasPrefix(line, "__OSB_BASE__=") {
			continue
		}
		// Parse "path\tsize" format.
		var p string
		var size int64
		if tabIdx := strings.IndexByte(line, '\t'); tabIdx >= 0 {
			p = line[:tabIdx]
			size, _ = strconv.ParseInt(line[tabIdx+1:], 10, 64)
		} else {
			p = line
		}
		clean := path.Clean(p)
		// Defence-in-depth: filter on the Go side too, in case the
		// shell-level case check was bypassed by a crafted path.
		if !pathUnder(clean, wsPath) {
			continue
		}
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, fileSearchResult{path: clean, size: size})
		if len(out) > maxCollectFiles {
			break
		}
	}
	return out, nil
}
