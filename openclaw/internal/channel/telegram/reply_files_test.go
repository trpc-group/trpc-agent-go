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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

func TestReplyFileCandidates_RecognizesExplicitPaths(t *testing.T) {
	t.Parallel()

	got := replyFileCandidates(
		"已生成 `out_pdf_split/风雨独立路_第3页.pdf` 和 " +
			"`frames/page2.png`，可直接发送。",
	)
	require.Contains(t, got, "out_pdf_split/风雨独立路_第3页.pdf")
	require.Contains(t, got, "frames/page2.png")
}

func TestReplyFileCandidates_DedupesPathAndInline(t *testing.T) {
	t.Parallel()

	got := replyFileCandidates(
		"发回 `frames/page2.png` 和 `frames/page2.png`",
	)
	require.Len(t, got, 1)
	require.Equal(t, "frames/page2.png", got[0])
}

func TestReplyFileCandidates_IgnoresBareFilenames(t *testing.T) {
	t.Parallel()

	got := replyFileCandidates(
		"已生成 风雨独立路_第3页.pdf 和 page2.png，可直接发送。",
	)
	require.Empty(t, got)
}

func TestReplyFileCandidates_RecognizesDirectoryCue(t *testing.T) {
	t.Parallel()

	got := replyFileCandidates("文件在目录 out_pdf_split 里，马上发回。")
	require.Contains(t, got, "out_pdf_split")
}

func TestReplyFileCandidates_RecognizesMediaDirectives(t *testing.T) {
	t.Parallel()

	got := replyFileCandidates(
		"处理完成。\n" +
			"MEDIA: /tmp/out split/page 1.png\n" +
			"MEDIA_DIR: /tmp/out split/pages",
	)
	require.Contains(t, got, "/tmp/out split/page 1.png")
	require.Contains(t, got, "/tmp/out split/pages")
}

func TestResolveReplyCandidateFiles_DirectRefs(t *testing.T) {
	t.Parallel()

	got := resolveReplyCandidateFiles(
		"artifact://reports/out.pdf@0",
		nil,
	)
	require.Len(t, got, 1)
	require.Equal(t, "artifact://reports/out.pdf@0", got[0].Path)

	got = resolveReplyCandidateFiles(
		"workspace://images/page1.png",
		nil,
	)
	require.Len(t, got, 1)
	require.Equal(t, "workspace://images/page1.png", got[0].Path)
}

func TestResolveReplyCandidateFiles_SearchesRoots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "out_pdf_split")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	want := filepath.Join(nested, "page2.png")
	require.NoError(t, os.WriteFile(want, []byte("png"), 0o600))

	got := resolveReplyCandidateFiles("page2.png", []string{root})
	require.Len(t, got, 1)
	require.Equal(t, want, got[0].Path)
}

func TestResolveReplyCandidateFiles_ExpandsDirectoryCueRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "out_pdf_split")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	fileA := filepath.Join(dir, "page3.pdf")
	fileB := filepath.Join(dir, "page4.pdf")
	require.NoError(t, os.WriteFile(fileA, []byte("a"), 0o600))
	require.NoError(t, os.WriteFile(fileB, []byte("b"), 0o600))

	got := resolveReplyCandidateFiles("out_pdf_split", []string{root})
	require.Len(t, got, 2)
	require.Equal(t, fileA, got[0].Path)
	require.Equal(t, fileB, got[1].Path)
}

func TestResolveReplyCandidateFiles_HostDirExpands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "a.pdf"),
		[]byte("a"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "b.pdf"),
		[]byte("b"),
		0o600,
	))

	got := resolveReplyCandidateFiles("host://"+root, []string{root})
	require.Len(t, got, 2)
}

func TestChannelCollectReplyFiles_DoesNotReuseBareSessionUploadNames(
	t *testing.T,
) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   channelID,
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	saved, err := store.Save(
		context.Background(),
		scope,
		"split.pdf",
		[]byte("%PDF-1.4"),
	)
	require.NoError(t, err)

	ch := &Channel{state: stateDir}
	got := ch.collectReplyFiles(
		"已生成 `split.pdf`，现在发回给你。",
		"u1",
		"telegram:dm:u1:s1",
	)
	require.Empty(t, got)
	require.NotEmpty(t, saved.Path)
}

func TestChannelCollectReplyFiles_UsesBareDerivedSessionUploadNames(
	t *testing.T,
) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   channelID,
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	saved, err := store.SaveWithInfo(
		context.Background(),
		scope,
		"split.pdf",
		uploads.FileMetadata{
			MimeType: "application/pdf",
			Source:   uploads.SourceDerived,
		},
		[]byte("%PDF-1.4"),
	)
	require.NoError(t, err)

	ch := &Channel{state: stateDir}
	got := ch.collectReplyFiles(
		"已生成 `split.pdf`，现在发回给你。",
		"u1",
		"telegram:dm:u1:s1",
	)
	require.Len(t, got, 1)
	require.Equal(t, cleanReplyFilePath(saved.Path), got[0].Path)
}

func TestChannelCollectReplyFiles_UsesBareGeneratedCurrentDirNames(
	t *testing.T,
) {
	t.Parallel()

	root := t.TempDir()
	framePath := filepath.Join(root, "page2.png")
	require.NoError(t, os.WriteFile(framePath, []byte("png"), 0o600))

	cwdMu.Lock()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	defer func() {
		require.NoError(t, os.Chdir(oldWD))
		cwdMu.Unlock()
	}()

	ch := &Channel{state: filepath.Join(root, "state")}
	got := ch.collectReplyFiles(
		"已生成 `page2.png`，现在发回。",
		"u1",
		"telegram:dm:u1:s1",
	)
	require.Len(t, got, 1)
	require.Equal(
		t,
		cleanReplyFilePath(framePath),
		got[0].Path,
	)
}

func TestChannelCollectReplyFiles_UsesExplicitDerivedPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	outDir := filepath.Join(root, "out_pdf_split")
	require.NoError(t, os.MkdirAll(outDir, 0o755))
	want := filepath.Join(
		outDir,
		"风雨独立路--李光耀回忆录_第3页.pdf",
	)
	require.NoError(t, os.WriteFile(want, []byte("%PDF-1.4"), 0o600))

	cwdMu.Lock()
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	defer func() {
		require.NoError(t, os.Chdir(oldWD))
		cwdMu.Unlock()
	}()

	ch := &Channel{state: filepath.Join(root, "state")}
	got := ch.collectReplyFiles(
		"已拆分成 `out_pdf_split/风雨独立路--李光耀回忆录_第3页.pdf`，"+
			"现在发给你。",
		"u1",
		"telegram:dm:u1:s1",
	)
	require.Len(t, got, 1)
	resolvedWant, err := filepath.EvalSymlinks(want)
	require.NoError(t, err)
	require.Equal(t, resolvedWant, got[0].Path)
}

func TestChannelCollectReplyFiles_UsesMediaDirectivePaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outDir := filepath.Join(root, "out pdf split")
	require.NoError(t, os.MkdirAll(outDir, 0o755))
	want := filepath.Join(outDir, "page 1.png")
	require.NoError(t, os.WriteFile(want, []byte("png"), 0o600))

	ch := &Channel{state: filepath.Join(root, "state")}
	got := ch.collectReplyFiles(
		"处理完成。\nMEDIA: "+want,
		"u1",
		"telegram:dm:u1:s1",
	)
	require.Len(t, got, 1)
	require.Equal(t, cleanReplyFilePath(want), got[0].Path)
}

func TestFindReplyNamedFiles_RespectsDepth(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	deepDir := filepath.Join(root, "a", "b", "c")
	require.NoError(t, os.MkdirAll(deepDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(deepDir, "deep.pdf"),
		[]byte("x"),
		0o600,
	))

	shallowDir := filepath.Join(root, "a", "b")
	require.NoError(t, os.WriteFile(
		filepath.Join(shallowDir, "shallow.pdf"),
		[]byte("x"),
		0o600,
	))

	got := findReplyNamedFiles(root, "deep.pdf", 2, 8)
	require.Empty(t, got)

	got = findReplyNamedFiles(root, "shallow.pdf", 2, 8)
	require.Len(t, got, 1)
}

func TestAutoReplyRoots_IncludeSessionUploadsRoot(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	roots := autoReplyRoots(
		stateDir,
		"u1",
		"telegram:dm:u1:s1",
	)
	require.NotEmpty(t, roots)

	want := sessionUploadsRoot(
		stateDir,
		"u1",
		"telegram:dm:u1:s1",
	)
	require.Contains(t, roots, filepath.Clean(want))
	require.Len(t, roots, 2)
	require.NotContains(t, roots, filepath.Clean(stateDir))
}
