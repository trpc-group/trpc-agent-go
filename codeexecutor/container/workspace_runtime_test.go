//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package container

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tcontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	testCID        = "cid"
	testExec1      = "e1"
	testExec2      = "e2"
	testRunBase    = "/mnt/run"
	contentHello   = "hello world"
	contentCollect = "filedata"
	waitShortSec   = 5
	b64PathStat    = "eyJuYW1lIjogIndoYXRldmVyIiwgInNpemUiOiAxMiwg"
	b64PathStat2   = "Im1vZGUiOiAzMzE4OCwgIm10aW1lIjogIjIwMjQtMDEtMDFU"
	b64PathStat3   = "MDA6MDA6MDBaIiwgImxpbmtUYXJnZXQiOiAiIn0="
)

// helper: fake docker client bound to httptest server.
func fakeDocker(t *testing.T, h http.HandlerFunc) (*client.Client, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	parsed, err := url.Parse(srv.URL)
	require.NoError(t, err)
	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://"+parsed.Host),
		client.WithVersion("1.46"),
	)
	require.NoError(t, err)
	cleanup := func() {
		_ = cli.Close()
		srv.Close()
	}
	return cli, cleanup
}

func TestWorkspaceRuntime_CreateAndCleanup(t *testing.T) {
	var execIdx int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/containers/"+testCID+"/exec"):
			// create exec id
			execIdx++
			id := testExec1
			if execIdx > 1 {
				id = testExec2
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + id + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/exec/"+testExec2+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "/exec/"+testExec2+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	rt := &WorkspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{
			runHostBase:      t.TempDir(),
			runContainerBase: testRunBase,
		},
	}

	ws, err := rt.CreateWorkspace(
		context.Background(), "abc 123", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(ws.Path, testRunBase))

	require.NoError(t, rt.Cleanup(context.Background(), ws))
}

func TestWorkspaceRuntime_PutFilesAndRun(t *testing.T) {
	var putCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut &&
			strings.Contains(r.URL.Path, "/containers/"+testCID+"/archive"):
			// verify incoming tar contains expected file
			tr := tar.NewReader(r.Body)
			hdr, err := tr.Next()
			require.NoError(t, err)
			require.Equal(t, "hello.txt", hdr.Name)
			b, err := io.ReadAll(tr)
			require.NoError(t, err)
			require.Contains(t, string(b), "hello")
			putCalled = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/containers/"+testCID+"/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "run-out", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	rt := &WorkspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{
			runHostBase:      t.TempDir(),
			runContainerBase: testRunBase,
		},
	}

	ws := codeexecutor.Workspace{ID: "w1", Path: path.Join(testRunBase, "w1")}
	err := rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{
		{Path: "hello.txt", Content: []byte(contentHello), Mode: 0o644},
	})
	require.NoError(t, err)
	require.True(t, putCalled)

	rr, err := rt.RunProgram(
		context.Background(), ws,
		codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", "echo ok"},
			Env:     map[string]string{"FOO": "BAR"},
			Timeout: time.Duration(waitShortSec) * time.Second,
		},
	)
	require.NoError(t, err)
	require.Equal(t, 0, rr.ExitCode)
	require.Contains(t, rr.Stdout, "run-out")
}

func TestWorkspaceRuntime_RunProgram_InsertsWorkspaceEnv(t *testing.T) {
	// Capture the ExecCreate request to inspect constructed shell.
	var capturedCmd []string
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
			// Accept staged files
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			// Decode ExecCreate payload to grab Cmd
			var payload struct {
				Cmd []string `json:"Cmd"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			capturedCmd = payload.Cmd
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "OUT", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	rt := &WorkspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{
			runHostBase:      t.TempDir(),
			runContainerBase: testRunBase,
		},
	}
	ws := codeexecutor.Workspace{ID: "wENV", Path: path.Join(testRunBase, "wENV")}
	// Stage a dummy file to allow cd into workspace.
	err := rt.PutFiles(context.Background(), ws,
		[]codeexecutor.PutFile{{Path: "d.txt", Content: []byte("x"),
			Mode: 0o644}})
	require.NoError(t, err)

	// Run without explicit WORKSPACE_DIR; runtime should inject it.
	_, err = rt.RunProgram(context.Background(), ws,
		codeexecutor.RunProgramSpec{Cmd: "bash", Args: []string{"-lc", "true"}})
	require.NoError(t, err)
	require.NotEmpty(t, capturedCmd)
	// Join the command string array; the env is embedded in -lc string.
	joined := strings.Join(capturedCmd, " ")
	require.Contains(t, joined, codeexecutor.WorkspaceEnvDirKey+"=")
	require.Contains(t, joined, ws.Path)
}

func TestWorkspaceRuntime_Collect(t *testing.T) {
	// will list two files and return tar streams for each
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/containers/"+testCID+"/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			// echo file list
			list := "/work/out/a.txt\n/work/other.png\n"
			writeHijackStream(t, conn, buf, list, "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "/containers/"+testCID+"/archive"):
			// Return a tar stream with a single file entry and stat header.
			w.Header().Set(
				"X-Docker-Container-Path-Stat",
				b64PathStat+b64PathStat2+b64PathStat3,
			)
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			_ = tw.WriteHeader(&tar.Header{
				Name: "whatever",
				Mode: 0o644,
				Size: int64(len(contentCollect)),
			})
			_, _ = tw.Write([]byte(contentCollect))
			_ = tw.Close()
			_, _ = w.Write(buf.Bytes())
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	rt := &WorkspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{runContainerBase: testRunBase},
	}

	ws := codeexecutor.Workspace{ID: "w2", Path: "/work"}
	files, err := rt.Collect(
		context.Background(), ws,
		[]string{"out/*.txt", "other.png"},
	)
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, "out/a.txt", files[0].Name)
	require.Contains(t, files[0].Content, contentCollect)
}

func TestWorkspaceRuntime_MountOptimizations(t *testing.T) {
	// This covers mount-first branches in PutDirectory and PutSkill.
	var execCalls int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/containers/"+testCID+"/exec"):
			execCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	skillsRoot := t.TempDir()
	// host paths inside the mounted skills root
	dir := filepath.Join(skillsRoot, "x")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	rt := &WorkspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{
			runContainerBase:    testRunBase,
			skillsHostBase:      skillsRoot,
			skillsContainerBase: "/mnt/skills",
		},
	}
	ws := codeexecutor.Workspace{ID: "w3", Path: path.Join(testRunBase, "w3")}

	// PutDirectory uses mount-first path
	require.NoError(t, rt.PutDirectory(
		context.Background(), ws, dir, "dst",
	))

	// PutSkill uses mount-first path
	require.NoError(t, rt.PutSkill(
		context.Background(), ws, dir, "dst2",
	))
	require.GreaterOrEqual(t, execCalls, 2)
}

func TestWorkspaceRuntime_ExecuteInline(t *testing.T) {
	var execCount int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut &&
			strings.Contains(r.URL.Path, "/containers/"+testCID+"/archive"):
			// accept staged file
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/containers/"+testCID+"/exec"):
			execCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "X", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	rt := &WorkspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{
			runHostBase:      t.TempDir(),
			runContainerBase: testRunBase,
		},
	}

	res, err := rt.ExecuteInline(
		context.Background(), "execID",
		[]codeexecutor.CodeBlock{{
			Code:     "echo hi",
			Language: "bash",
		}}, time.Second*3,
	)
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode)
	require.Contains(t, res.Stdout, "X")
	require.GreaterOrEqual(t, execCount, 1)
}

func TestHelpers_Sanitize_ShellQuote_TarFromFiles(t *testing.T) {
	// sanitize
	require.Equal(t, "abc_123", sanitize("abc 123"))
	require.Equal(t, "AZaz09-__", sanitize("AZaz09-!@"))

	// shellQuote
	require.Equal(t, "''", shellQuote(""))
	sq := shellQuote("a'b")
	require.True(t, strings.HasPrefix(sq, "'a'"))
	require.Contains(t, sq, "\\'")
	require.True(t, strings.HasSuffix(sq, "b'"))

	// tarFromFiles
	rc, err := tarFromFiles([]codeexecutor.PutFile{{
		Path:    "p/q.txt",
		Content: []byte("x"),
		Mode:    0o644,
	}})
	require.NoError(t, err)
	defer rc.Close()
	tr := tar.NewReader(rc)
	hdr, err := tr.Next()
	require.NoError(t, err)
	require.Equal(t, "p/q.txt", hdr.Name)

	// invalid path
	_, err = tarFromFiles([]codeexecutor.PutFile{{Path: "."}})
	require.Error(t, err)
}
