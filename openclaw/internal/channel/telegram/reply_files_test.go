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

func TestReplyFileCandidates_RecognizesPlainFilenames(t *testing.T) {
	t.Parallel()

	got := replyFileCandidates(
		"已生成 风雨独立路_第3页.pdf 和 page2.png，可直接发送。",
	)
	require.Contains(t, got, "风雨独立路_第3页.pdf")
	require.Contains(t, got, "page2.png")
}

func TestReplyFileCandidates_DedupesPlainAndInline(t *testing.T) {
	t.Parallel()

	got := replyFileCandidates("发回 `page2.png` 和 page2.png")
	require.Len(t, got, 1)
	require.Equal(t, "page2.png", got[0])
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

func TestChannelCollectReplyFiles_UsesSessionUploads(t *testing.T) {
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
	require.Len(t, got, 1)
	require.Equal(t, saved.Path, got[0].Path)
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
}
