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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/bmatcuk/doublestar/v4"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// collectListDepth bounds the recursive directory listing used by
// Collect. execd walks up to this many levels below the workspace root;
// deeper files are not collected. 256 is far beyond any output tree a
// program writes on purpose, and keeps the request explicit rather than
// relying on a server default.
const collectListDepth = 256

// fileInfoTypeFile is the execd FileInfo.Type for a regular file. execd
// reports lstat types ("file", "directory", "symlink", "other"), so a
// symlink is never mistaken for the file it points at.
const fileInfoTypeFile = "file"

// Collect returns files in the workspace that match the supplied
// globs. The workspace tree is listed once through the SDK's
// ListDirectoryWithDepth (execd `/directories/list`), matched against
// the patterns on the host, and read back through DownloadFile sized
// against maxReadSizeBytes.
//
// Only entries execd reports as regular files are collected. Symlinks
// are skipped outright (even when their target is inside the
// workspace), which is stricter than the container executor's
// readlink-and-check behaviour: DownloadFile follows symlinks, so a
// lstat-based type filter is the only check that cannot be raced by
// swapping the link target between listing and download.
//
// The listing is a single JSON response holding one FileInfo per
// entry under the workspace, so host memory scales with the number of
// entries (roughly 250 bytes each), not with file contents. Collect
// then applies maxCollectFiles / maxCollectTotalBytes before any
// content is downloaded.
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
	globs, err := compileCollectGlobs(patterns)
	if err != nil {
		return nil, err
	}
	if len(globs) == 0 {
		return nil, nil
	}
	sb, err := r.sandbox()
	if err != nil {
		return nil, err
	}

	entries, err := sb.ListDirectoryWithDepth(ctx, ws.Path, collectListDepth)
	if err != nil {
		return nil, fmt.Errorf("opensandbox: list workspace files: %w", err)
	}
	matches, listingCapped, err := selectCollectMatches(entries, ws.Path, globs)
	if err != nil {
		return nil, err
	}

	out := make([]codeexecutor.File, 0, len(matches))
	var totalBytes int64
	// limitsHit only when an eligible file is actually skipped (or the
	// listing was capped). Filling the budget exactly with the last
	// match is complete collection, not partial.
	limitsHit := listingCapped
	for _, m := range matches {
		remaining := maxCollectTotalBytes - totalBytes
		if remaining <= 0 {
			limitsHit = true
			break
		}
		if remaining > maxReadSizeBytes {
			remaining = maxReadSizeBytes
		}
		data, size, truncated, err := r.readFile(ctx, sb, m.path, remaining, m.size)
		if err != nil {
			return nil, err
		}
		totalBytes += int64(len(data))
		out = append(out, codeexecutor.File{
			Name:      m.rel,
			Content:   string(data),
			MIMEType:  http.DetectContentType(data),
			SizeBytes: size,
			Truncated: truncated,
		})
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

// collectMatch is one regular file selected for download: its absolute
// sandbox path, the workspace-relative name reported to the caller, and
// the size from the listing (used for SizeBytes when content is capped).
type collectMatch struct {
	path string
	rel  string
	size int64
}

// compileCollectGlobs turns caller patterns into doublestar globs that
// are matched against workspace-relative POSIX paths. A pattern without
// a path separator is prefixed with "**/" so it matches by basename at
// any depth (the same rule the bash globstar listing applied); a pattern
// with a separator is matched against the relative path as written, so
// "out/*.csv" only matches directly under out/. Invalid globs are
// rejected rather than silently matching nothing.
func compileCollectGlobs(patterns []string) ([]string, error) {
	globs := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, "./")
		if p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			p = "**/" + p
		}
		if !doublestar.ValidatePattern(p) {
			return nil, fmt.Errorf("opensandbox: invalid collect pattern %q", p)
		}
		globs = append(globs, p)
	}
	return globs, nil
}

// selectCollectMatches filters a workspace listing down to the regular
// files that match any glob, in listing order, deduplicated by relative
// path and capped at maxCollectFiles. It returns capped=true when at
// least one further eligible file was left out.
//
// Entries are trusted only when execd reports a type: a listing without
// type metadata cannot distinguish a symlink from a file, so it is
// refused instead of being collected blindly. Entries outside wsPath
// are ignored defensively even though execd never returns them.
func selectCollectMatches(
	entries []osb.FileInfo, wsPath string, globs []string,
) ([]collectMatch, bool, error) {
	prefix := strings.TrimSuffix(wsPath, "/") + "/"
	seen := make(map[string]bool, len(entries))
	out := make([]collectMatch, 0, len(entries))
	for _, e := range entries {
		if e.Type == "" {
			return nil, false, errors.New(
				"opensandbox: workspace listing has no file type metadata; " +
					"the execd version in the sandbox image is too old for Collect",
			)
		}
		if e.Type != fileInfoTypeFile {
			continue
		}
		if !strings.HasPrefix(e.Path, prefix) {
			continue
		}
		rel := e.Path[len(prefix):]
		if rel == "" || seen[rel] {
			continue
		}
		if codeexecutor.IsRootMetadataTempPath(rel) {
			continue
		}
		if !matchesAnyGlob(globs, rel) {
			continue
		}
		seen[rel] = true
		if len(out) >= maxCollectFiles {
			return out, true, nil
		}
		out = append(out, collectMatch{path: e.Path, rel: rel, size: e.Size})
	}
	return out, false, nil
}

// matchesAnyGlob reports whether rel matches at least one compiled glob.
// Patterns were validated by compileCollectGlobs, so a match error here
// is impossible and treated as no match.
func matchesAnyGlob(globs []string, rel string) bool {
	for _, g := range globs {
		if ok, err := doublestar.Match(g, rel); err == nil && ok {
			return true
		}
	}
	return false
}

// errDeclarativeIONotSupported is the package-private sentinel returned
// by the StageInputs/CollectOutputs v1 stubs. It is deliberately not
// exported: the root-module capability plumbing (a public
// errors.Is-checkable sentinel plus engine capability advertisement)
// lands in a follow-up PR; this minimal executor advertises the missing
// surface through the error message and callers fall back to
// PutFiles/Collect.
var errDeclarativeIONotSupported = errors.New(
	"opensandbox: declarative I/O (StageInputs/CollectOutputs) " +
		"not supported in v1; use PutFiles/Collect",
)

// StageInputs maps external inputs into the sandbox workspace.
//
// Not implemented in v1; returns errDeclarativeIONotSupported.
func (r *workspaceRuntime) StageInputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	_ = ctx
	_ = ws
	_ = specs
	return errDeclarativeIONotSupported
}

// CollectOutputs applies the declarative output spec in the sandbox.
//
// Not implemented in v1; returns errDeclarativeIONotSupported.
func (r *workspaceRuntime) CollectOutputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	_ = ctx
	_ = ws
	_ = spec
	return codeexecutor.OutputManifest{}, errDeclarativeIONotSupported
}

// readFile reads up to limit bytes from a remote path via the SDK's
// DownloadFile API. Returns the data, the file's full size (which
// may exceed len(data) when truncated), and a truncated flag. knownSize
// is the real file size from the listing metadata; when positive it is
// used as the returned size so callers get an accurate SizeBytes even
// for files larger than limit. When knownSize is non-positive, the size
// falls back to the number of bytes actually read (capped at limit+1).
//
// The returned size is always at least len(data) to prevent stale
// metadata from making SizeBytes smaller than the actual content read.
//
// Truncated is true only when the read actually hit the limit+1 cap
// (proving the file is at least limit+1 bytes). This avoids false
// positives when the file shrank between listing and DownloadFile:
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
	// the file shrank between listing and DownloadFile.
	truncated := readBytes > limit
	if truncated {
		data = data[:limit]
	}
	// Prefer the real size from the listing metadata; fall back to
	// the byte count we actually read when metadata is unavailable.
	// Use max(knownSize, readBytes) — readBytes (before truncation)
	// so hitting the limit+1 detection is reflected in size even when
	// knownSize is stale (file grew between listing and download).
	size := knownSize
	if size < readBytes {
		size = readBytes
	}
	return data, size, truncated, nil
}
