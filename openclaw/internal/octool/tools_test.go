//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package octool

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
	sessionpkg "trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func newExecCommandTool(mgr *Manager) tool.CallableTool {
	return NewExecCommandTool(mgr).(tool.CallableTool)
}

func newWriteStdinTool(mgr *Manager) tool.CallableTool {
	return NewWriteStdinTool(mgr).(tool.CallableTool)
}

func newKillSessionTool(mgr *Manager) tool.CallableTool {
	return NewKillSessionTool(mgr).(tool.CallableTool)
}

func TestExecTool_Foreground(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager()
	tool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": "echo hello",
		"yieldMs": 0,
	})
	out, err := tool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	require.Contains(t, res.Output, "hello")
	require.Equal(t, 0, res.ExitCode)
}

func TestAnnotateExecResult_ParsesMediaMarkers(t *testing.T) {
	t.Parallel()

	res := execResult{
		Status: "exited",
		Output: "done\nMEDIA: /tmp/a.png\n" +
			"MEDIA_DIR: /tmp/out frames\n" +
			"MEDIA: /tmp/a.png\n",
	}
	annotateExecResult(&res)
	require.Equal(t, []string{"/tmp/a.png"}, res.MediaFiles)
	require.Equal(t, []string{"/tmp/out frames"}, res.MediaDirs)
}

func TestMapPollResult_IncludesMediaMarkers(t *testing.T) {
	t.Parallel()

	code := 0
	out := mapPollResult("sess-1", processPoll{
		Status:     "exited",
		Output:     "MEDIA: page1.png\nMEDIA_DIR: out_pdf_split",
		Offset:     1,
		NextOffset: 3,
		ExitCode:   &code,
	})
	require.Equal(t, []string{"page1.png"}, out["media_files"])
	require.Equal(
		t,
		[]string{"out_pdf_split"},
		out["media_dirs"],
	)
}

func TestExecTool_YieldBackgroundAndPoll(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": "echo start; sleep 0.2; echo end",
		"yieldMs": 10,
	})
	out, err := execTool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)

	const (
		pollDeadline = 2 * time.Second
		pollInterval = 50 * time.Millisecond
	)
	deadline := time.Now().Add(pollDeadline)
	var all string
	for time.Now().Before(deadline) {
		poll, err := mgr.poll(res.SessionID, nil)
		require.NoError(t, err)
		if poll.Output != "" {
			all += "\n" + poll.Output
		}
		if poll.Status == "exited" {
			require.Contains(t, all, "start")
			require.Contains(t, all, "end")
			return
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("process did not exit; output: %s", all)
}

func TestProcessTool_Submit(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)
	writeTool := newWriteStdinTool(mgr)

	args := mustJSON(t, map[string]any{
		"command":    `read -r line; echo got:$line`,
		"background": true,
	})
	out, err := execTool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)

	submitArgs := mustJSON(t, map[string]any{
		"session_id":     res.SessionID,
		"chars":          "hi",
		"append_newline": true,
	})
	writeAny, err := writeTool.Call(context.Background(), submitArgs)
	require.NoError(t, err)
	writeRes := writeAny.(map[string]any)
	all := outputField(writeRes)

	const (
		pollDeadline = 2 * time.Second
		pollInterval = 50 * time.Millisecond
	)
	deadline := time.Now().Add(pollDeadline)
	var exited bool
	for time.Now().Before(deadline) {
		poll, err := mgr.poll(res.SessionID, nil)
		require.NoError(t, err)
		if poll.Output != "" {
			all += "\n" + poll.Output
		}
		if poll.Status == "exited" {
			exited = true
			if strings.Contains(all, "got:hi") {
				return
			}
		}
		time.Sleep(pollInterval)
	}
	if exited {
		t.Fatalf("process exited; output: %s", all)
	}
	t.Fatalf("process did not exit; output: %s", all)
}

func TestExecTool_PTYForeground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty is not supported on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager()
	tool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command": "echo hi",
		"pty":     true,
		"yieldMs": 0,
	})
	out, err := tool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "exited", res.Status)
	require.Contains(t, res.Output, "hi")
	require.Equal(t, 0, res.ExitCode)
}

func TestManager_MaxLinesTrimsOutput(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10*time.Second), WithMaxLines(1))
	execTool := newExecCommandTool(mgr)

	args := mustJSON(t, map[string]any{
		"command":    "printf 'a\\nb\\nc\\n'",
		"background": true,
	})
	out, err := execTool.Call(context.Background(), args)
	require.NoError(t, err)

	res := out.(execResult)
	require.Equal(t, "running", res.Status)
	require.NotEmpty(t, res.SessionID)

	pollUntilExited(t, mgr, res.SessionID)

	logAny, err := mgr.log(res.SessionID, nil, nil)
	require.NoError(t, err)

	log := logAny
	require.Equal(t, "c", strings.TrimSpace(log.Output))
}

func TestProcessTool_ListKillClearRemove(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)
	killTool := newKillSessionTool(mgr)

	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "sleep 5",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(execResult)
	require.NotEmpty(t, res.SessionID)

	err = mgr.clearFinished(res.SessionID)
	require.Error(t, err)

	list := map[string]any{
		"sessions": mgr.list(),
	}
	sessions := list["sessions"].([]processSession)
	require.NotEmpty(t, sessions)

	_, err = killTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"session_id": res.SessionID,
		}),
	)
	require.NoError(t, err)

	pollUntilExited(t, mgr, res.SessionID)

	err = mgr.clearFinished(res.SessionID)
	require.NoError(t, err)

	_, err = mgr.poll("", nil)
	require.Error(t, err)

	err = mgr.remove("missing")
	require.Error(t, err)

	out, err = execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "echo bye",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res = out.(execResult)
	err = mgr.remove(res.SessionID)
	require.NoError(t, err)
}

func TestManager_MergedEnvAndExitCode(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	env := mergedEnv(map[string]string{
		"FOO":  "bar",
		"PATH": "testpath",
	})
	require.NotNil(t, env)
	require.Contains(t, env, "FOO=bar")
	require.Contains(t, env, "PATH=testpath")

	err := exec.Command("bash", "-lc", "exit 7").Run()
	require.Error(t, err)
	require.Equal(t, 7, exitCode(err))
}

func TestResolveWorkdir(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	wd, err := resolveWorkdir("")
	require.NoError(t, err)
	require.Empty(t, wd)

	wd, err = resolveWorkdir("~")
	require.NoError(t, err)
	require.Equal(t, home, wd)

	wd, err = resolveWorkdir("~/x")
	require.NoError(t, err)
	require.Equal(t, filepath.ToSlash(home)+"/x", filepath.ToSlash(wd))
}

func TestManager_CleanupExpiredRemovesFinished(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(1 * time.Nanosecond))
	execTool := newExecCommandTool(mgr)

	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "echo done",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(execResult)
	pollUntilExited(t, mgr, res.SessionID)

	sess, err := mgr.get(res.SessionID)
	require.NoError(t, err)
	doneAt := sess.doneAt()
	mgr.clock = func() time.Time {
		return doneAt.Add(10 * time.Second)
	}

	sessions := mgr.list()
	require.Empty(t, sessions)
}

func TestExitCode_NonExitError(t *testing.T) {
	require.Equal(t, -1, exitCode(errors.New("x")))
}

func TestProcessTool_Write(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)
	writeTool := newWriteStdinTool(mgr)

	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "read -r x; echo got:$x",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(execResult)
	writeAny, err := writeTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"session_id": res.SessionID,
			"chars":      "ok\n",
		}),
	)
	require.NoError(t, err)

	output := outputField(writeAny.(map[string]any))
	output += pollUntilExited(t, mgr, res.SessionID)
	require.Contains(t, output, "got:ok")
}

func TestTools_InvalidArgs(t *testing.T) {
	mgr := NewManager()
	execTool := newExecCommandTool(mgr)
	_, err := execTool.Call(context.Background(), []byte("{"))
	require.Error(t, err)

	writeTool := newWriteStdinTool(mgr)
	_, err = writeTool.Call(context.Background(), []byte("{"))
	require.Error(t, err)
}

func TestSortSessions_SortsBySessionID(t *testing.T) {
	s := []processSession{
		{SessionID: "b"},
		{SessionID: "a"},
	}
	sortSessions(s)
	require.Equal(t, "a", s[0].SessionID)
	require.Equal(t, "b", s[1].SessionID)
}

func TestTools_Declaration(t *testing.T) {
	mgr := NewManager()
	execTool := newExecCommandTool(mgr)
	writeTool := newWriteStdinTool(mgr)
	killTool := newKillSessionTool(mgr)

	require.Equal(t, toolExecCommand, execTool.Declaration().Name)
	require.Equal(t, toolWriteStdin, writeTool.Declaration().Name)
	require.Equal(t, toolKillSession, killTool.Declaration().Name)
}

func TestManager_ListIncludesExitedSession(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)

	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "echo hi",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(execResult)
	pollUntilExited(t, mgr, res.SessionID)

	sessions := mgr.list()
	require.NotEmpty(t, sessions)
	require.Equal(t, "exited", sessions[0].Status)
}

func TestManager_RemoveRunningSession(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	mgr := NewManager(WithJobTTL(10 * time.Second))
	execTool := newExecCommandTool(mgr)

	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "sleep 5",
			"background": true,
		}),
	)
	require.NoError(t, err)

	res := out.(execResult)
	err = mgr.remove(res.SessionID)
	require.NoError(t, err)

	sessions := mgr.list()
	require.Empty(t, sessions)
}

func TestStartPipes_ErrorWhenStdioSet(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	t.Run("stdin set", func(t *testing.T) {
		cmd := exec.Command("bash", "-lc", "echo ok")
		cmd.Stdin = strings.NewReader("x")
		_, _, _, err := startPipes(cmd)
		require.Error(t, err)
	})

	t.Run("stdout set", func(t *testing.T) {
		cmd := exec.Command("bash", "-lc", "echo ok")
		cmd.Stdout = io.Discard
		_, _, _, err := startPipes(cmd)
		require.Error(t, err)
	})

	t.Run("stderr set", func(t *testing.T) {
		cmd := exec.Command("bash", "-lc", "echo ok")
		cmd.Stderr = io.Discard
		_, _, _, err := startPipes(cmd)
		require.Error(t, err)
	})
}

func TestStartPTY_NilCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty is not supported on windows")
	}
	_, _, err := startPTY(nil)
	require.Error(t, err)
}

func TestSession_TailAllOutputAndMarkDone(t *testing.T) {
	s := newSession("id", "cmd", 0)

	require.Empty(t, s.tail(0))

	s.appendOutput("a\nb")
	require.Equal(t, "a\nb", s.tail(10))

	out, code := s.allOutput()
	require.Equal(t, "a\nb", out)
	require.Equal(t, 0, code)

	s.markDone(7)
	out, code = s.allOutput()
	require.Equal(t, "a\nb", out)
	require.Equal(t, 7, code)

	s.markDone(9)
	_, code = s.allOutput()
	require.Equal(t, 7, code)

	snap := s.snapshot()
	require.Equal(t, "exited", snap.Status)
	require.NotNil(t, snap.ExitCode)
	require.Equal(t, 7, *snap.ExitCode)
}

func TestSession_Log(t *testing.T) {
	s := newSession("id", "cmd", 0)

	total := defaultLogLimit + 50
	for i := 0; i < total; i++ {
		s.appendOutput("x\n")
	}

	got := s.log(nil, nil)
	require.Equal(t, 50, got.Offset)
	require.Equal(t, total, got.NextOffset)
	require.Len(t, strings.Split(got.Output, "\n"), defaultLogLimit)

	offset := 999
	got = s.log(&offset, nil)
	require.Empty(t, got.Output)
	require.Equal(t, total, got.Offset)
	require.Equal(t, total, got.NextOffset)

	offset = 20
	limit := 2
	got = s.log(&offset, &limit)
	require.Len(t, strings.Split(got.Output, "\n"), 2)
	require.Equal(t, offset, got.Offset)
	require.Equal(t, offset+limit, got.NextOffset)
}

func TestTools_NilManagers(t *testing.T) {
	execTool := newExecCommandTool(nil)
	_, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"command": "echo hi"}),
	)
	require.Error(t, err)

	writeTool := newWriteStdinTool(nil)
	_, err = writeTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"session_id": "x"}),
	)
	require.Error(t, err)
}

func TestManager_ExecErrors(t *testing.T) {
	mgr := NewManager()

	_, err := mgr.Exec(nil, execParams{Command: "echo hi"})
	require.Error(t, err)

	_, err = mgr.Exec(context.Background(), execParams{})
	require.Error(t, err)
}

func TestUploadEnvFromContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "report.pdf")
	audioPath := filepath.Join(dir, "clip.ogg")
	videoPath := filepath.Join(dir, "movie.mp4")
	imagePath := filepath.Join(dir, "frame.png")
	require.NoError(t, os.WriteFile(
		filePath,
		[]byte("pdf"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		audioPath,
		[]byte("ogg"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		videoPath,
		[]byte("mp4"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		imagePath,
		[]byte("png"),
		0o600,
	))

	userMsg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name:     "report.pdf",
					FileID:   "host://" + filePath,
					MimeType: "application/pdf",
				},
			},
			{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name:     "frame.png",
					FileID:   "host://" + imagePath,
					MimeType: "image/png",
				},
			},
		},
	}
	currentMsg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name:     "clip.ogg",
					FileID:   "host://" + audioPath,
					MimeType: "audio/ogg",
				},
			},
			{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name:     "movie.mp4",
					FileID:   "host://" + videoPath,
					MimeType: "video/mp4",
				},
			},
		},
	}
	ev := event.NewResponseEvent("inv", "user", &model.Response{
		Choices: []model.Choice{{Message: userMsg}},
	})
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(currentMsg),
		agent.WithInvocationSession(
			&sessionpkg.Session{
				Events: []event.Event{*ev},
			},
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	env := (&execTool{}).uploadEnvFromContext(ctx)
	require.Equal(t, videoPath, env[envLastUploadPath])
	require.Equal(t, uploads.HostRef(videoPath), env[envLastUploadHostRef])
	require.Equal(t, dir, env[envSessionUploadsDir])
	require.Equal(t, "movie.mp4", env[envLastUploadName])
	require.Equal(
		t,
		"video/mp4",
		env[envLastUploadMIME],
	)
	require.Equal(t, audioPath, env[envLastAudioPath])
	require.Equal(t, uploads.HostRef(audioPath), env[envLastAudioHostRef])
	require.Equal(t, "clip.ogg", env[envLastAudioName])
	require.Equal(t, "audio/ogg", env[envLastAudioMIME])
	require.Equal(t, videoPath, env[envLastVideoPath])
	require.Equal(t, uploads.HostRef(videoPath), env[envLastVideoHostRef])
	require.Equal(t, "movie.mp4", env[envLastVideoName])
	require.Equal(t, "video/mp4", env[envLastVideoMIME])
	require.Equal(t, imagePath, env[envLastImagePath])
	require.Equal(t, uploads.HostRef(imagePath), env[envLastImageHostRef])
	require.Equal(t, "frame.png", env[envLastImageName])
	require.Equal(t, "image/png", env[envLastImageMIME])
	require.Equal(t, filePath, env[envLastPDFPath])
	require.Equal(t, uploads.HostRef(filePath), env[envLastPDFHostRef])
	require.Equal(t, "report.pdf", env[envLastPDFName])
	require.Equal(
		t,
		"application/pdf",
		env[envLastPDFMIME],
	)

	var recent []execUploadMeta
	require.NoError(
		t,
		json.Unmarshal([]byte(env[envRecentUploadsJSON]), &recent),
	)
	require.Len(t, recent, 4)
	require.Equal(t, videoPath, recent[0].Path)
	require.Equal(t, uploadKindVideo, recent[0].Kind)
	require.Equal(t, audioPath, recent[1].Path)
	require.Equal(t, uploadKindAudio, recent[1].Kind)
	require.Equal(t, imagePath, recent[2].Path)
	require.Equal(t, uploadKindImage, recent[2].Kind)
	require.Equal(t, filePath, recent[3].Path)
	require.Equal(t, uploadKindPDF, recent[3].Kind)
}

func TestUploadEnvFromContext_UsesUploadStore(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	derived, err := store.SaveWithInfo(
		context.Background(),
		scope,
		"split-page-3.pdf",
		uploads.FileMetadata{
			MimeType: "application/pdf",
			Source:   uploads.SourceDerived,
		},
		[]byte("%PDF-1.4"),
	)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			sessionpkg.NewSession(
				"app",
				"u1",
				"telegram:dm:u1:s1",
			),
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	env := (&execTool{uploads: store}).uploadEnvFromContext(ctx)
	require.Equal(t, derived.Path, env[envLastUploadPath])
	require.Equal(t, derived.HostRef, env[envLastUploadHostRef])
	require.Equal(t, derived.Path, env[envLastPDFPath])
	require.Equal(t, derived.HostRef, env[envLastPDFHostRef])
	require.Equal(
		t,
		filepath.Dir(derived.Path),
		env[envSessionUploadsDir],
	)

	var recent []execUploadMeta
	require.NoError(
		t,
		json.Unmarshal([]byte(env[envRecentUploadsJSON]), &recent),
	)
	require.Len(t, recent, 1)
	require.Equal(t, derived.Path, recent[0].Path)
	require.Equal(t, uploadKindPDF, recent[0].Kind)
	require.Equal(t, uploads.SourceDerived, recent[0].Source)
}

func TestUploadEnvFromContext_RewritesGeneratedUploadNames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	videoPath := filepath.Join(dir, "file_10.mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("mp4"), 0o600))

	msg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeFile,
			File: &model.File{
				Name:     "file_10.mp4",
				FileID:   "host://" + videoPath,
				MimeType: "video/mp4",
			},
		}},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(msg),
		agent.WithInvocationSession(
			sessionpkg.NewSession("app", "u1", "telegram:dm:u1:s1"),
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	env := (&execTool{}).uploadEnvFromContext(ctx)
	require.Equal(t, "video.mp4", env[envLastUploadName])
	require.Equal(t, "video.mp4", env[envLastVideoName])

	var recent []execUploadMeta
	require.NoError(
		t,
		json.Unmarshal([]byte(env[envRecentUploadsJSON]), &recent),
	)
	require.Len(t, recent, 1)
	require.Equal(t, "video.mp4", recent[0].Name)
}

func TestUploadEnvFromContext_UsesSessionDirWithoutRecentUploads(
	t *testing.T,
) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			sessionpkg.NewSession(
				"app",
				"u1",
				"telegram:dm:u1:s1",
			),
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	env := (&execTool{uploads: store}).uploadEnvFromContext(ctx)
	require.Equal(
		t,
		store.ScopeDir(uploads.Scope{
			Channel:   "telegram",
			UserID:    "u1",
			SessionID: "telegram:dm:u1:s1",
		}),
		env[envSessionUploadsDir],
	)
	require.NotContains(t, env, envLastUploadPath)
}

func TestUploadKindFromMeta(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		uploadKindImage,
		uploadKindFromMeta("frame.png", ""),
	)
	require.Equal(
		t,
		uploadKindAudio,
		uploadKindFromMeta("voice.bin", "audio/ogg"),
	)
	require.Equal(
		t,
		uploadKindVideo,
		uploadKindFromMeta("clip.mp4", ""),
	)
	require.Equal(
		t,
		uploadKindPDF,
		uploadKindFromMeta("report.pdf", ""),
	)
	require.Equal(
		t,
		uploadKindFile,
		uploadKindFromMeta("archive.bin", ""),
	)
}

func TestMergeExecEnv_PreservesExplicitEnv(t *testing.T) {
	t.Parallel()

	merged := mergeExecEnv(
		map[string]string{envLastUploadPath: "explicit"},
		map[string]string{
			envLastUploadPath: "derived",
			envLastUploadName: "report.pdf",
			envLastUploadMIME: "application/pdf",
		},
	)
	require.Equal(t, "explicit", merged[envLastUploadPath])
	require.Equal(t, "report.pdf", merged[envLastUploadName])
	require.Equal(
		t,
		"application/pdf",
		merged[envLastUploadMIME],
	)
}

func pollUntilExited(t *testing.T, mgr *Manager, id string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		pollAny, err := mgr.poll(id, nil)
		require.NoError(t, err)

		poll := pollAny
		if poll.Output != "" {
			out += "\n" + poll.Output
		}
		if poll.Status == "exited" {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process did not exit; output: %s", out)
	return ""
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func outputField(result map[string]any) string {
	value, ok := result["output"].(string)
	if !ok {
		return ""
	}
	return value
}
