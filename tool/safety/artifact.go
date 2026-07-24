//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

// redactArtifact redacts a single artifact in place. For text artifacts
// (text/*, application/json, application/yaml, application/x-yaml), it
// replaces secret substrings in Data, Name, and URL. For binary or
// unknown MIME types, it rejects a secret-bearing artifact rather than
// corrupting bytes. The Name and URL fields are always redacted
// regardless of MIME type because they can carry credentials (e.g.
// `https://user:pass@host/` or `name=API_KEY=sk_live_...`).
//
// It returns a new artifact (so callers can keep the original), whether
// any redaction was applied, and an error when a binary secret-bearing
// artifact cannot be safely returned.
func redactArtifact(in *artifact.Artifact) (*artifact.Artifact, bool, error) {
	if in == nil {
		return nil, false, nil
	}
	changed := false
	clone := *in
	clone.Data = append([]byte(nil), in.Data...)

	// Redact Name and URL regardless of MIME type.
	if nameRedacted, c := redactString(clone.Name); c {
		clone.Name = nameRedacted
		changed = true
	}
	if urlRedacted, c := redactString(clone.URL); c {
		clone.URL = urlRedacted
		changed = true
	}

	if isTextMIME(clone.MimeType) {
		if isJSONMIME(clone.MimeType) {
			out, c, err := redactJSONArtifact(clone.Data)
			if err != nil {
				return nil, false, err
			}
			if c {
				clone.Data = out
				changed = true
			}
		} else {
			out, c := redactString(string(clone.Data))
			if c {
				clone.Data = []byte(out)
				changed = true
			}
		}
		if !changed {
			return &clone, false, nil
		}
		return &clone, true, nil
	}

	// Binary: check Data for secrets. Also check Name/URL which were
	// already redacted above.
	if hasSecret(string(clone.Data)) {
		return nil, true, errors.New(
			"binary artifact contains a secret in its data; refusing to persist or return it")
	}
	if !changed {
		return &clone, false, nil
	}
	return &clone, true, nil
}

func isJSONMIME(mime string) bool {
	low := normalizedMediaType(mime)
	return low == "application/json" || low == "application/x-json" ||
		strings.HasSuffix(low, "+json")
}

func redactJSONArtifact(data []byte) ([]byte, bool, error) {
	decoded, err := decodeJSONValue(data)
	if err != nil {
		return nil, false, errors.New(
			"json artifact could not be decoded safely",
		)
	}
	safe, changed, err := redactValue(decoded)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return data, false, nil
	}
	raw, err := json.Marshal(safe)
	if err != nil {
		return nil, false, errors.New(
			"redacted json artifact could not be encoded safely",
		)
	}
	return raw, true, nil
}

// isTextMIME returns true for MIME types whose contents can be safely
// redacted as UTF-8 text.
func isTextMIME(mime string) bool {
	low := normalizedMediaType(mime)
	if low == "" {
		return false
	}
	if strings.HasPrefix(low, "text/") {
		return true
	}
	switch low {
	case "application/json", "application/yaml",
		"application/x-yaml", "application/x-json",
		"application/xml", "application/javascript",
		"application/x-sh", "application/x-shellscript":
		return true
	}
	if strings.HasSuffix(low, "+json") || strings.HasSuffix(low, "+yaml") {
		return true
	}
	return false
}

func normalizedMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		mediaType = strings.TrimSpace(strings.SplitN(value, ";", 2)[0])
	}
	return strings.ToLower(mediaType)
}

// artifactServiceWrapper wraps an artifact.Service so SaveArtifact and
// LoadArtifact redact or refuse secret-bearing artifacts before they
// reach the underlying storage. ListArtifactKeys, DeleteArtifact, and
// ListVersions are passed through unchanged.
type artifactServiceWrapper struct {
	inner artifact.Service
}

// newArtifactServiceWrapper returns an artifact.Service that applies the
// guard's redaction policy on SaveArtifact and LoadArtifact.
func newArtifactServiceWrapper(inner artifact.Service) artifact.Service {
	if inner == nil {
		return nil
	}
	return &artifactServiceWrapper{inner: inner}
}

// SaveArtifact implements artifact.Service. A filename that itself
// contains a secret is rejected rather than persisted, because the
// filename becomes a storage key that ListArtifactKeys would expose.
func (w *artifactServiceWrapper) SaveArtifact(
	ctx context.Context,
	info artifact.SessionInfo,
	filename string,
	a *artifact.Artifact,
) (int, error) {
	if hasSecret(filename) {
		return 0, errors.New(
			"artifact filename contains a secret; refusing to persist it")
	}
	safe, _, err := redactArtifact(a)
	if err != nil {
		return 0, err
	}
	return w.inner.SaveArtifact(ctx, info, filename, safe)
}

// LoadArtifact implements artifact.Service.
func (w *artifactServiceWrapper) LoadArtifact(
	ctx context.Context,
	info artifact.SessionInfo,
	filename string,
	version *int,
) (*artifact.Artifact, error) {
	loaded, err := w.inner.LoadArtifact(ctx, info, filename, version)
	if err != nil {
		return nil, err
	}
	safe, _, err := redactArtifact(loaded)
	if err != nil {
		return nil, err
	}
	return safe, nil
}

// ListArtifactKeys implements artifact.Service.
func (w *artifactServiceWrapper) ListArtifactKeys(
	ctx context.Context,
	info artifact.SessionInfo,
) ([]string, error) {
	keys, err := w.inner.ListArtifactKeys(ctx, info)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if hasSecret(key) {
			return nil, errors.New(
				"artifact key contains a secret; refusing to list artifacts",
			)
		}
	}
	return keys, nil
}

// DeleteArtifact implements artifact.Service.
func (w *artifactServiceWrapper) DeleteArtifact(
	ctx context.Context,
	info artifact.SessionInfo,
	filename string,
) error {
	return w.inner.DeleteArtifact(ctx, info, filename)
}

// ListVersions implements artifact.Service.
func (w *artifactServiceWrapper) ListVersions(
	ctx context.Context,
	info artifact.SessionInfo,
	filename string,
) ([]int, error) {
	return w.inner.ListVersions(ctx, info, filename)
}
