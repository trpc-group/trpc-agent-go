//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceinput

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type stubFS struct {
	collectFiles []codeexecutor.File
	collectErr   error
	putErr       error
	putCalls     int
	putFiles     []codeexecutor.PutFile
}

func (s *stubFS) PutFiles(
	_ context.Context,
	_ codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	s.putCalls++
	s.putFiles = append(s.putFiles, files...)
	return s.putErr
}

func (*stubFS) StageDirectory(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ string,
	_ string,
	_ codeexecutor.StageOptions,
) error {
	return nil
}

func (s *stubFS) Collect(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ []string,
) ([]codeexecutor.File, error) {
	if s.collectErr != nil {
		return nil, s.collectErr
	}
	return s.collectFiles, nil
}

func (*stubFS) StageInputs(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ []codeexecutor.InputSpec,
) error {
	return nil
}

func (*stubFS) CollectOutputs(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

type stubRunner struct {
	res      codeexecutor.RunResult
	err      error
	calls    int
	lastSpec codeexecutor.RunProgramSpec
}

func (r *stubRunner) RunProgram(
	_ context.Context,
	_ codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	r.calls++
	r.lastSpec = spec
	return r.res, r.err
}

type stubDownloadModel struct {
	data    []byte
	mime    string
	err     error
	fileIDs []string
}

func (m *stubDownloadModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (*stubDownloadModel) Info() model.Info {
	return model.Info{Name: "stub"}
}

func (m *stubDownloadModel) DownloadFile(
	_ context.Context,
	fileID string,
) ([]byte, string, error) {
	m.fileIDs = append(m.fileIDs, fileID)
	return m.data, m.mime, m.err
}

func newInvocationCtx(
	msg model.Message,
	sess *session.Session,
	mdl model.Model,
	svc artifact.Service,
) context.Context {
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(msg),
		agent.WithInvocationSession(sess),
		agent.WithInvocationModel(mdl),
		agent.WithInvocationArtifactService(svc),
	)
	return agent.NewInvocationContext(context.Background(), inv)
}

func userFileEvent(files ...model.File) event.Event {
	parts := make([]model.ContentPart, 0, len(files))
	for _, f := range files {
		fileCopy := f
		parts = append(parts, model.ContentPart{
			Type: model.ContentTypeFile,
			File: &fileCopy,
		})
	}
	return event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:         model.RoleUser,
					ContentParts: parts,
				},
			}},
		},
	}
}

func TestStageConversationFiles_Warnings(t *testing.T) {
	msg := model.NewUserMessage("process the file")
	msg.AddFileData("report.txt", []byte("hello"), "text/plain")
	ws := codeexecutor.Workspace{ID: "ws"}

	t.Run("load metadata warning", func(t *testing.T) {
		ctx := newInvocationCtx(msg, nil, nil, nil)
		eng := codeexecutor.NewEngine(nil, &stubFS{
			collectErr: fmt.Errorf("collect failed"),
		}, nil)

		staged, warnings := StageConversationFiles(ctx, eng, ws)
		require.Nil(t, staged)
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0], "load metadata")
	})

	t.Run("put files warning", func(t *testing.T) {
		ctx := newInvocationCtx(msg, nil, nil, nil)
		fs := &stubFS{putErr: fmt.Errorf("put failed")}
		eng := codeexecutor.NewEngine(nil, fs, nil)

		staged, warnings := StageConversationFiles(ctx, eng, ws)
		require.Nil(t, staged)
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0], "stage files")
		require.Equal(t, 1, fs.putCalls)
		require.Len(t, fs.putFiles, 1)
		require.Equal(
			t,
			path.Join(codeexecutor.DirWork, "inputs", "report.txt"),
			fs.putFiles[0].Path,
		)
	})

	t.Run("save metadata warning", func(t *testing.T) {
		ctx := newInvocationCtx(msg, nil, nil, nil)
		fs := &stubFS{}
		runner := &stubRunner{err: fmt.Errorf("move failed")}
		eng := codeexecutor.NewEngine(nil, fs, runner)

		staged, warnings := StageConversationFiles(ctx, eng, ws)
		require.Len(t, staged, 1)
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0], "save metadata")
		require.Equal(t, 2, fs.putCalls)
		require.Equal(
			t,
			path.Join(codeexecutor.DirWork, "inputs", "report.txt"),
			staged[0].Name,
		)
		require.Equal(t, "report.txt", staged[0].OriginalName)
		require.Equal(t, "text/plain", staged[0].MIMEType)
		require.Equal(t, int64(5), staged[0].SizeBytes)
	})
}

func TestStageConversationFile_DefaultNameAndMetadataReuse(t *testing.T) {
	ctx := context.Background()
	f := model.File{Data: []byte("hello")}

	t.Run("default fallback and de-dup", func(t *testing.T) {
		puts := []codeexecutor.PutFile{}
		md := &codeexecutor.WorkspaceMetadata{}
		existingTo := map[string]struct{}{
			path.Join(codeexecutor.DirWork, "inputs", "upload_1"): {},
		}

		item, warn := stageConversationFile(
			ctx,
			nil,
			f,
			0,
			map[string]struct{}{},
			existingTo,
			map[string]string{},
			&puts,
			md,
		)
		require.Empty(t, warn)
		require.NotNil(t, item)
		require.Equal(
			t,
			path.Join(codeexecutor.DirWork, "inputs", "upload_1_2"),
			item.Name,
		)
		require.Equal(t, "upload_1", item.OriginalName)
		require.Len(t, puts, 1)
		require.Len(t, md.Inputs, 1)
	})

	t.Run("reuse staged path from metadata key", func(t *testing.T) {
		key, ok := reuseKey(f, "report.txt")
		require.True(t, ok)

		puts := []codeexecutor.PutFile{}
		item, warn := stageConversationFile(
			ctx,
			nil,
			model.File{Name: "report.txt", Data: f.Data},
			0,
			map[string]struct{}{},
			map[string]struct{}{},
			map[string]string{
				key: path.Join(codeexecutor.DirWork, "inputs", "cached.txt"),
			},
			&puts,
			&codeexecutor.WorkspaceMetadata{},
		)
		require.Empty(t, warn)
		require.NotNil(t, item)
		require.Equal(
			t,
			path.Join(codeexecutor.DirWork, "inputs", "cached.txt"),
			item.Name,
		)
		require.Equal(t, "report.txt", item.OriginalName)
		require.Empty(t, puts)
	})

	t.Run("same bytes but different names do not collapse", func(t *testing.T) {
		key, ok := reuseKey(f, "cached.txt")
		require.True(t, ok)

		puts := []codeexecutor.PutFile{}
		item, warn := stageConversationFile(
			ctx,
			nil,
			model.File{Name: "renamed.txt", Data: f.Data},
			0,
			map[string]struct{}{},
			map[string]struct{}{},
			map[string]string{
				key: path.Join(codeexecutor.DirWork, "inputs", "cached.txt"),
			},
			&puts,
			&codeexecutor.WorkspaceMetadata{},
		)
		require.Empty(t, warn)
		require.NotNil(t, item)
		require.Equal(
			t,
			path.Join(codeexecutor.DirWork, "inputs", "renamed.txt"),
			item.Name,
		)
		require.Len(t, puts, 1)
	})
}

func TestFilesFromSessionAndMessage_Filtering(t *testing.T) {
	file := model.File{Name: "report.txt", Data: []byte("x")}
	text := "ignore me"

	sess := session.NewSession(
		"app",
		"user",
		"sess",
		session.WithSessionEvents([]event.Event{
			{},
			{
				Response: &model.Response{
					Choices: []model.Choice{{
						Message: model.Message{
							Role: model.RoleAssistant,
							ContentParts: []model.ContentPart{{
								Type: model.ContentTypeFile,
								File: &file,
							}},
						},
					}},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{{
						Message: model.Message{
							Role: model.RoleUser,
							ContentParts: []model.ContentPart{{
								Type: model.ContentTypeText,
								Text: &text,
							}},
						},
					}},
				},
			},
			userFileEvent(file),
		}),
	)

	got := filesFromSession(sess)
	require.Len(t, got, 1)
	require.Equal(t, file.Name, got[0].Name)

	msg := model.NewUserMessage("hi")
	msg.ContentParts = append(msg.ContentParts,
		model.ContentPart{Type: model.ContentTypeText, Text: &text},
		model.ContentPart{Type: model.ContentTypeFile, File: &file},
	)
	got = filesFromMessage(msg)
	require.Len(t, got, 1)
	require.Equal(t, file.Name, got[0].Name)
}

func TestArtifactBaseNameAndPathHelpers(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "report.txt")
	require.NoError(t, os.WriteFile(tempFile, []byte("hi"), 0o600))

	require.Equal(
		t,
		"report.txt",
		ArtifactBaseName("artifact://uploads/report.txt@3"),
	)
	require.Empty(t, ArtifactBaseName("file://uploads/report.txt"))
	require.Empty(t, ArtifactBaseName("artifact://@3"))

	require.Equal(t, "report.txt", SanitizeFileName(`dir\report.txt`))
	require.Equal(t, DefaultName, SanitizeFileName(" ../ "))

	hostPath, ok := hostPathFromID(HostPrefix + tempFile)
	require.True(t, ok)
	require.Equal(t, tempFile, hostPath)

	hostPath, ok = hostPathFromID(tempFile)
	require.True(t, ok)
	require.Equal(t, tempFile, hostPath)

	_, ok = hostPathFromID("host://relative.txt")
	require.False(t, ok)
	_, ok = hostPathFromID("relative.txt")
	require.False(t, ok)
}

func TestResolveFileBytes_CoversInputSources(t *testing.T) {
	t.Run("missing ref", func(t *testing.T) {
		data, mime, warn := ResolveFileBytes(context.Background(), nil, model.File{})
		require.Nil(t, data)
		require.Empty(t, mime)
		require.Equal(t, WarnMissingRef, warn)
	})

	t.Run("host path", func(t *testing.T) {
		hostPath := filepath.Join(t.TempDir(), "host.txt")
		require.NoError(t, os.WriteFile(hostPath, []byte("host-bytes"), 0o600))

		data, mime, warn := ResolveFileBytes(context.Background(), nil, model.File{
			FileID:   HostPrefix + hostPath,
			MimeType: "text/plain",
		})
		require.Equal(t, []byte("host-bytes"), data)
		require.Equal(t, "text/plain", mime)
		require.Empty(t, warn)
	})

	t.Run("downloader", func(t *testing.T) {
		mdl := &stubDownloadModel{
			data: []byte("remote"),
			mime: "text/csv",
		}
		data, mime, warn := ResolveFileBytes(
			context.Background(),
			mdl,
			model.File{FileID: "provider-file-1"},
		)
		require.Equal(t, []byte("remote"), data)
		require.Equal(t, "text/csv", mime)
		require.Empty(t, warn)
		require.Equal(t, []string{"provider-file-1"}, mdl.fileIDs)
	})

	t.Run("no downloader", func(t *testing.T) {
		data, mime, warn := ResolveFileBytes(
			context.Background(),
			nil,
			model.File{FileID: "provider-file-2"},
		)
		require.Nil(t, data)
		require.Empty(t, mime)
		require.Equal(t, WarnNoDownloader, warn)
	})

	t.Run("artifact no service", func(t *testing.T) {
		data, mime, warn := ResolveFileBytes(
			context.Background(),
			nil,
			model.File{FileID: "artifact://uploads/report.txt@1"},
		)
		require.Nil(t, data)
		require.Empty(t, mime)
		require.Equal(t, WarnArtifactNoService, warn)
	})

	t.Run("artifact parse error", func(t *testing.T) {
		ctx := codeexecutor.WithArtifactService(context.Background(), inmemory.NewService())
		data, mime, warn := ResolveFileBytes(
			ctx,
			nil,
			model.File{FileID: "artifact://@1"},
		)
		require.Nil(t, data)
		require.Empty(t, mime)
		require.Contains(t, warn, "parse artifact ref")
	})

	t.Run("artifact via invocation context", func(t *testing.T) {
		svc := inmemory.NewService()
		info := artifact.SessionInfo{
			AppName:   "app",
			UserID:    "user",
			SessionID: "sess",
		}
		ver, err := svc.SaveArtifact(
			context.Background(),
			info,
			"uploads/report.txt",
			&artifact.Artifact{
				Data:     []byte("artifact-data"),
				MimeType: "text/plain",
				Name:     "report.txt",
			},
		)
		require.NoError(t, err)

		ref := fmt.Sprintf("artifact://uploads/report.txt@%d", ver)
		ctx := newInvocationCtx(
			model.NewUserMessage("read"),
			session.NewSession("app", "user", "sess"),
			nil,
			svc,
		)

		data, mime, warn := ResolveFileBytes(
			ctx,
			nil,
			model.File{FileID: ref},
		)
		require.Equal(t, []byte("artifact-data"), data)
		require.Equal(t, "text/plain", mime)
		require.Empty(t, warn)
	})
}

func TestStageConversationFiles_MergesSessionAndMessageFiles(t *testing.T) {
	fs := &stubFS{}
	runner := &stubRunner{}
	eng := codeexecutor.NewEngine(nil, fs, runner)
	ws := codeexecutor.Workspace{ID: "ws"}

	sessionFile := model.File{Name: "session.txt", Data: []byte("from session")}
	message := model.NewUserMessage("include current file too")
	message.AddFileData("message.txt", []byte("from message"), "text/plain")
	sess := session.NewSession(
		"app",
		"user",
		"sess",
		session.WithSessionEvents([]event.Event{userFileEvent(sessionFile)}),
	)

	ctx := newInvocationCtx(message, sess, nil, nil)
	staged, warnings := StageConversationFiles(ctx, eng, ws)

	require.Len(t, staged, 2)
	require.Empty(t, warnings)
	require.Equal(
		t,
		path.Join(codeexecutor.DirWork, "inputs", "session.txt"),
		staged[0].Name,
	)
	require.Equal(t, "session.txt", staged[0].OriginalName)
	require.Equal(
		t,
		path.Join(codeexecutor.DirWork, "inputs", "message.txt"),
		staged[1].Name,
	)
	require.Equal(t, "message.txt", staged[1].OriginalName)
	require.Len(t, fs.putFiles, 3)
	require.Equal(
		t,
		path.Join(codeexecutor.DirWork, "inputs", "session.txt"),
		fs.putFiles[0].Path,
	)
	require.Equal(
		t,
		path.Join(codeexecutor.DirWork, "inputs", "message.txt"),
		fs.putFiles[1].Path,
	)

	var md codeexecutor.WorkspaceMetadata
	require.NoError(t, json.Unmarshal(fs.putFiles[2].Content, &md))
	require.Len(t, md.Inputs, 2)
	require.Equal(
		t,
		path.Join(codeexecutor.DirWork, "inputs", "session.txt"),
		md.Inputs[0].To,
	)
	require.Equal(
		t,
		path.Join(codeexecutor.DirWork, "inputs", "message.txt"),
		md.Inputs[1].To,
	)
}

func TestStageConversationFiles_CoversEmptyAndReusePaths(t *testing.T) {
	ws := codeexecutor.Workspace{ID: "ws"}

	t.Run("no invocation", func(t *testing.T) {
		staged, warnings := StageConversationFiles(context.Background(), codeexecutor.NewEngine(nil, &stubFS{}, nil), ws)
		require.Nil(t, staged)
		require.Nil(t, warnings)
	})

	t.Run("no files", func(t *testing.T) {
		ctx := newInvocationCtx(model.NewUserMessage("no files"), nil, nil, nil)
		staged, warnings := StageConversationFiles(ctx, codeexecutor.NewEngine(nil, &stubFS{}, nil), ws)
		require.Nil(t, staged)
		require.Nil(t, warnings)
	})

	t.Run("metadata reuse skips put", func(t *testing.T) {
		file := model.File{Name: "report.txt", Data: []byte("same")}
		key, ok := reuseKey(file, "report.txt")
		require.True(t, ok)

		md := codeexecutor.WorkspaceMetadata{
			Version: 1,
			Inputs: []codeexecutor.InputRecord{{
				From: inputFromPrefix + key,
				To:   path.Join(codeexecutor.DirWork, "inputs", "report.txt"),
			}},
		}
		buf, err := json.Marshal(md)
		require.NoError(t, err)

		fs := &stubFS{collectFiles: []codeexecutor.File{{
			Name:    codeexecutor.MetaFileName,
			Content: string(buf),
		}}}
		msg := model.NewUserMessage("reuse")
		msg.ContentParts = append(msg.ContentParts, model.ContentPart{
			Type: model.ContentTypeFile,
			File: &file,
		})
		ctx := newInvocationCtx(msg, nil, nil, nil)

		staged, warnings := StageConversationFiles(ctx, codeexecutor.NewEngine(nil, fs, &stubRunner{}), ws)
		require.Len(t, staged, 1)
		require.Empty(t, warnings)
		require.Equal(
			t,
			path.Join(codeexecutor.DirWork, "inputs", "report.txt"),
			staged[0].Name,
		)
		require.Empty(t, fs.putFiles)
	})
}

func TestHelperCoverage_GapFillers(t *testing.T) {
	t.Run("fastKey", func(t *testing.T) {
		key, ok := fastKey(model.File{FileID: "provider-file"})
		require.True(t, ok)
		require.Contains(t, key, "file_id/provider-file")

		key, ok = fastKey(model.File{})
		require.False(t, ok)
		require.Empty(t, key)
	})

	t.Run("hostBytes error", func(t *testing.T) {
		data, mime, warn := hostBytes("/definitely/missing/file.txt", model.File{})
		require.Nil(t, data)
		require.Empty(t, mime)
		require.Contains(t, warn, "read host path")
	})

	t.Run("withArtifactContext", func(t *testing.T) {
		ctx := withArtifactContext(context.Background(), nil)
		_, ok := codeexecutor.ArtifactServiceFromContext(ctx)
		require.False(t, ok)

		inv := agent.NewInvocation(
			agent.WithInvocationSession(session.NewSession("app", "user", "sess")),
			agent.WithInvocationArtifactService(inmemory.NewService()),
		)
		ctx = withArtifactContext(context.Background(), inv)
		_, ok = codeexecutor.ArtifactServiceFromContext(ctx)
		require.True(t, ok)
	})
}
