//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package workspaceinput stages conversation file inputs into executor
// workspaces so workspace-bound tools can operate on uploaded files without
// exposing staging steps to the model.
package workspaceinput

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/internal/skillstage"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	inputFromPrefix = "user_message://"
	inputModePut    = "put"
	inputNameFmt    = "upload_%d"

	keyFileIDPrefix = "file_id/"
	keySHA256Prefix = "sha256/"

	// DefaultName is used when an uploaded file does not carry a usable name.
	DefaultName = "upload"
	// HostPrefix is the trusted host-file prefix used by local runtimes.
	HostPrefix = "host://"

	warnPrefix = "user file input:"
	// WarnMissingRef is returned when a file part contains neither bytes nor a
	// stable reference.
	WarnMissingRef = warnPrefix + " missing bytes and file_id"
	// WarnNoDownloader is returned when the current model cannot dereference a
	// provider-side file id.
	WarnNoDownloader = warnPrefix + " model does not support file download"
	// WarnArtifactNoService is returned when an artifact ref is present but no
	// artifact service is available in the current invocation context.
	WarnArtifactNoService = warnPrefix +
		" artifact service is not configured"
)

// StagedInput describes one conversation file materialized into the workspace.
type StagedInput struct {
	Name         string `json:"name"`
	OriginalName string `json:"original_name,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
}

// StageConversationFiles materializes conversation file inputs into
// work/inputs/ for the current invocation workspace.
func StageConversationFiles(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
) ([]StagedInput, []string) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, nil
	}

	files := filesFromSession(inv.Session)
	if msgFiles := filesFromMessage(inv.Message); len(msgFiles) > 0 {
		files = append(files, msgFiles...)
	}
	if len(files) == 0 {
		return nil, nil
	}

	stager := skillstage.New()
	md, err := stager.LoadWorkspaceMetadata(ctx, eng, ws)
	if err != nil {
		return nil, []string{
			fmt.Sprintf("%s load metadata: %v", warnPrefix, err),
		}
	}

	existingTo := make(map[string]struct{})
	existingByKey := make(map[string]string)
	for _, rec := range md.Inputs {
		to := strings.TrimSpace(rec.To)
		if to != "" {
			existingTo[to] = struct{}{}
		}
		if !strings.HasPrefix(rec.From, inputFromPrefix) || to == "" {
			continue
		}
		key := strings.TrimSpace(strings.TrimPrefix(rec.From, inputFromPrefix))
		if key != "" {
			existingByKey[key] = to
		}
	}

	usedNames := make(map[string]struct{})
	puts := make([]codeexecutor.PutFile, 0, len(files))
	staged := make([]StagedInput, 0, len(files))
	var warnings []string
	ctxIO := withArtifactContext(ctx, inv)

	for i, f := range files {
		item, warn := stageConversationFile(
			ctxIO,
			inv.Model,
			f,
			i,
			usedNames,
			existingTo,
			existingByKey,
			&puts,
			&md,
		)
		if warn != "" {
			warnings = append(warnings, warn)
		}
		if item != nil {
			staged = append(staged, *item)
		}
	}

	if len(puts) == 0 {
		return staged, warnings
	}
	if err := eng.FS().PutFiles(ctx, ws, puts); err != nil {
		return nil, []string{
			fmt.Sprintf("%s stage files: %v", warnPrefix, err),
		}
	}
	if err := stager.SaveWorkspaceMetadata(ctx, eng, ws, md); err != nil {
		warnings = append(warnings, fmt.Sprintf(
			"%s save metadata: %v",
			warnPrefix,
			err,
		))
	}
	return staged, warnings
}

// ArtifactBaseName extracts a stable basename from an artifact:// ref.
func ArtifactBaseName(fileID string) string {
	s := strings.TrimSpace(fileID)
	if !strings.HasPrefix(s, fileref.ArtifactPrefix) {
		return ""
	}
	rest := strings.TrimPrefix(s, fileref.ArtifactPrefix)
	name, _, err := codeexecutor.ParseArtifactRef(rest)
	if err != nil {
		return ""
	}
	base := path.Base(strings.TrimSpace(name))
	if base == "." || base == "/" || base == ".." {
		return ""
	}
	return base
}

// SanitizeFileName converts a user-provided filename into a safe basename.
func SanitizeFileName(name string) string {
	s := strings.TrimSpace(name)
	s = strings.ReplaceAll(s, "\\", "/")
	s = path.Base(path.Clean(s))
	if s == "." || s == ".." || s == "/" {
		return DefaultName
	}
	s = strings.TrimPrefix(s, "/")
	if strings.TrimSpace(s) == "" {
		return DefaultName
	}
	return s
}

// UniqueFileName de-duplicates a sanitized upload name under work/inputs.
func UniqueFileName(
	used map[string]struct{},
	existingTo map[string]struct{},
	name string,
) string {
	if strings.TrimSpace(name) == "" {
		name = DefaultName
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s_%d%s", base, i, ext)
		}
		key := strings.ToLower(candidate)
		if used != nil {
			if _, ok := used[key]; ok {
				continue
			}
		}
		to := path.Join(codeexecutor.DirWork, "inputs", candidate)
		if existingTo != nil {
			if _, ok := existingTo[to]; ok {
				continue
			}
		}
		if used != nil {
			used[key] = struct{}{}
		}
		return candidate
	}
}

// ResolveFileBytes resolves an uploaded file into bytes and a MIME type.
func ResolveFileBytes(
	ctx context.Context,
	mdl model.Model,
	f model.File,
) ([]byte, string, string) {
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
		ctx = withArtifactContext(ctx, inv)
	}
	if len(f.Data) > 0 {
		return f.Data, strings.TrimSpace(f.MimeType), ""
	}

	fileID := strings.TrimSpace(f.FileID)
	if fileID == "" {
		return nil, "", WarnMissingRef
	}
	if strings.HasPrefix(fileID, fileref.ArtifactPrefix) {
		return artifactBytes(ctx, fileID)
	}
	if hostPath, ok := hostPathFromID(fileID); ok {
		return hostBytes(hostPath, f)
	}

	dl, ok := mdl.(model.FileDownloader)
	if !ok || dl == nil {
		return nil, "", WarnNoDownloader
	}
	data, mime, err := dl.DownloadFile(ctx, fileID)
	if err != nil {
		return nil, "", fmt.Sprintf(
			"%s download %s: %v",
			warnPrefix,
			fileID,
			err,
		)
	}
	return data, mime, ""
}

func stageConversationFile(
	ctx context.Context,
	mdl model.Model,
	f model.File,
	idx int,
	usedNames map[string]struct{},
	existingTo map[string]struct{},
	existingByKey map[string]string,
	puts *[]codeexecutor.PutFile,
	md *codeexecutor.WorkspaceMetadata,
) (*StagedInput, string) {
	rawName := strings.TrimSpace(f.Name)
	if rawName == "" {
		rawName = ArtifactBaseName(f.FileID)
	}
	if rawName == "" {
		rawName = fmt.Sprintf(inputNameFmt, idx+1)
	}
	name := SanitizeFileName(rawName)

	key, hasKey := reuseKey(f, name)
	if hasKey {
		if to, ok := existingByKey[key]; ok {
			return &StagedInput{
				Name:         to,
				OriginalName: rawName,
			}, ""
		}
	}

	data, mime, warn := ResolveFileBytes(ctx, mdl, f)
	if warn != "" {
		return nil, warn
	}

	name = UniqueFileName(usedNames, existingTo, name)
	to := path.Join(codeexecutor.DirWork, "inputs", name)
	*puts = append(*puts, codeexecutor.PutFile{
		Path:    to,
		Content: data,
		Mode:    codeexecutor.DefaultScriptFileMode,
	})
	existingTo[to] = struct{}{}
	if hasKey {
		existingByKey[key] = to
	}
	if md != nil {
		from := inputFromPrefix
		if hasKey {
			from += key
		}
		md.Inputs = append(md.Inputs, codeexecutor.InputRecord{
			From:      from,
			To:        to,
			Resolved:  name,
			Mode:      inputModePut,
			Timestamp: time.Now(),
		})
	}
	return &StagedInput{
		Name:         to,
		OriginalName: rawName,
		MIMEType:     mime,
		SizeBytes:    int64(len(data)),
	}, ""
}

func fastKey(f model.File) (string, bool) {
	id := strings.TrimSpace(f.FileID)
	if id != "" {
		return keyFileIDPrefix + id, true
	}
	if len(f.Data) == 0 {
		return "", false
	}
	sum := sha256.Sum256(f.Data)
	return keySHA256Prefix + hex.EncodeToString(sum[:]), true
}

func reuseKey(f model.File, sanitizedName string) (string, bool) {
	key, ok := fastKey(f)
	if !ok {
		return "", false
	}
	name := strings.ToLower(SanitizeFileName(sanitizedName))
	return key + "/name/" + name, true
}

func filesFromSession(sess *session.Session) []model.File {
	if sess == nil {
		return nil
	}
	sess.EventMu.RLock()
	events := append([]event.Event(nil), sess.Events...)
	sess.EventMu.RUnlock()

	var out []model.File
	for _, ev := range events {
		if ev.Response == nil {
			continue
		}
		for _, choice := range ev.Response.Choices {
			if choice.Message.Role != model.RoleUser {
				continue
			}
			for _, part := range choice.Message.ContentParts {
				if part.Type != model.ContentTypeFile || part.File == nil {
					continue
				}
				out = append(out, *part.File)
			}
		}
	}
	return out
}

func filesFromMessage(msg model.Message) []model.File {
	if len(msg.ContentParts) == 0 {
		return nil
	}
	var out []model.File
	for _, part := range msg.ContentParts {
		if part.Type != model.ContentTypeFile || part.File == nil {
			continue
		}
		out = append(out, *part.File)
	}
	return out
}

func hostPathFromID(fileID string) (string, bool) {
	trimmed := strings.TrimSpace(fileID)
	if trimmed == "" {
		return "", false
	}
	if strings.HasPrefix(trimmed, HostPrefix) {
		hostPath := strings.TrimPrefix(trimmed, HostPrefix)
		if filepath.IsAbs(hostPath) {
			return hostPath, true
		}
		return "", false
	}
	if filepath.IsAbs(trimmed) {
		return trimmed, true
	}
	return "", false
}

func hostBytes(hostPath string, f model.File) ([]byte, string, string) {
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return nil, "", fmt.Sprintf(
			"%s read host path %s: %v",
			warnPrefix,
			hostPath,
			err,
		)
	}
	return data, strings.TrimSpace(f.MimeType), ""
}

func artifactBytes(
	ctx context.Context,
	fileID string,
) ([]byte, string, string) {
	ref := strings.TrimPrefix(fileID, fileref.ArtifactPrefix)
	name, ver, err := codeexecutor.ParseArtifactRef(ref)
	if err != nil {
		return nil, "", fmt.Sprintf(
			"%s parse artifact ref %s: %v",
			warnPrefix,
			fileID,
			err,
		)
	}
	if svc, ok := codeexecutor.ArtifactServiceFromContext(ctx); !ok || svc == nil {
		return nil, "", WarnArtifactNoService
	}
	data, mime, _, err := codeexecutor.LoadArtifactHelper(ctx, name, ver)
	if err != nil {
		return nil, "", fmt.Sprintf(
			"%s load artifact %s: %v",
			warnPrefix,
			fileID,
			err,
		)
	}
	return data, mime, ""
}

func withArtifactContext(
	ctx context.Context,
	inv *agent.Invocation,
) context.Context {
	if inv == nil {
		return ctx
	}
	if inv.ArtifactService != nil {
		ctx = codeexecutor.WithArtifactService(ctx, inv.ArtifactService)
	}
	if inv.Session == nil {
		return ctx
	}
	return codeexecutor.WithArtifactSession(ctx, artifact.SessionInfo{
		AppName:   inv.Session.AppName,
		UserID:    inv.Session.UserID,
		SessionID: inv.Session.ID,
	})
}
