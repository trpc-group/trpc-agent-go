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
	"fmt"
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

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	testCID        = "cid"
	testExec1      = "e1"
	testExec2      = "e2"
	testRunBase    = "/tmp/run"
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
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			// create exec id
			execIdx++
			id := testExec1
			if execIdx > 1 {
				id = testExec2
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + id + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec2+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec2+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	rt := &workspaceRuntime{
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
		context.Background(), "abc 123",
		codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(ws.Path, testRunBase))

	require.NoError(t, rt.Cleanup(context.Background(), ws))
}

func TestWorkspaceRuntime_CreateWorkspace_AutoMapsInputs(t *testing.T) {
	host := t.TempDir()
	var cmds [][]string
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			var payload struct {
				Cmd []string `json:"Cmd"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			cmds = append(cmds, payload.Cmd)
			id := fmt.Sprintf("e%d", len(cmds))
			w.Header().Set("Content-Type",
				"application/json")
			_, _ = w.Write([]byte(`{"Id":"` + id + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/exec/") &&
			strings.Contains(r.URL.Path, "/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "/exec/") &&
			strings.Contains(r.URL.Path, "/json"):
			w.Header().Set("Content-Type",
				"application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	ce := &CodeExecutor{
		client:    cli,
		container: &tcontainer.Summary{ID: testCID},
		hostConfig: tcontainer.HostConfig{
			Binds: []string{
				host + ":" + defaultInputsContainer + ":ro",
			},
		},
		autoInputs: true,
	}
	rt, err := newWorkspaceRuntime(ce)
	require.NoError(t, err)

	ws, err := rt.CreateWorkspace(
		context.Background(), "auto-map",
		codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	require.NotEmpty(t, ws.Path)
	require.GreaterOrEqual(t, len(cmds), 2)
	linkCmd := strings.Join(cmds[1], " ")
	require.Contains(t, linkCmd, "ln -sfn")
	require.Contains(t, linkCmd, defaultInputsContainer)
	require.Contains(t, linkCmd, path.Join(
		codeexecutor.DirWork, "inputs",
	))
}

func TestWorkspaceRuntime_PutFilesAndRun(t *testing.T) {
	var putCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
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
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "run-out", "")
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

	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{
			runHostBase:      t.TempDir(),
			runContainerBase: testRunBase,
		},
	}

	ws := codeexecutor.Workspace{ID: "w1",
		Path: path.Join(testRunBase, "w1")}
	err := rt.PutFiles(context.Background(), ws, []codeexecutor.PutFile{
		{Path: "hello.txt", Content: []byte(contentHello),
			Mode: 0o644},
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

	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{
			runHostBase:      t.TempDir(),
			runContainerBase: testRunBase,
		},
	}
	ws := codeexecutor.Workspace{ID: "wENV",
		Path: path.Join(testRunBase, "wENV")}
	// Stage a dummy file to allow cd into workspace.
	err := rt.PutFiles(context.Background(), ws,
		[]codeexecutor.PutFile{{Path: "d.txt",
			Content: []byte("x"), Mode: 0o644}})
	require.NoError(t, err)

	// Run without explicit WORKSPACE_DIR; runtime should inject it.
	_, err = rt.RunProgram(context.Background(), ws,
		codeexecutor.RunProgramSpec{Cmd: "bash",
			Args: []string{"-lc", "true"}})
	require.NoError(t, err)
	require.NotEmpty(t, capturedCmd)
	// Join the command string array; the env is embedded in -lc.
	joined := strings.Join(capturedCmd, " ")
	require.Contains(t, joined,
		codeexecutor.WorkspaceEnvDirKey+"=")
	require.Contains(t, joined, ws.Path)
}

func TestWorkspaceRuntime_Collect(t *testing.T) {
	// will list two files and return tar streams for each
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			// echo file list
			list := "/work/out/a.txt\n/work/other.png\n"
			writeHijackStream(t, conn, buf, list, "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
			// Return a tar stream with a single file entry and stat.
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

	rt := &workspaceRuntime{
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
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			execCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
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

	skillsRoot := t.TempDir()
	// host paths inside the mounted skills root
	dir := filepath.Join(skillsRoot, "x")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{
			runContainerBase:    testRunBase,
			skillsHostBase:      skillsRoot,
			skillsContainerBase: "/opt/trpc-agent/skills",
		},
	}
	ws := codeexecutor.Workspace{ID: "w3",
		Path: path.Join(testRunBase, "w3")}

	// PutDirectory uses mount-first path
	require.NoError(t, rt.PutDirectory(
		context.Background(), ws, dir, "dst",
	))

	// StageDirectory also uses mount-first path
	require.NoError(t, rt.StageDirectory(
		context.Background(), ws, dir, "dst2",
		codeexecutor.StageOptions{ReadOnly: true, AllowMount: true},
	))
	require.GreaterOrEqual(t, execCalls, 2)
}

func TestWorkspaceRuntime_PutDirectory_EmptyError(t *testing.T) {
	rt := &workspaceRuntime{}
	ws := codeexecutor.Workspace{ID: "w", Path: "/w"}
	err := rt.PutDirectory(context.Background(), ws, "", "dst")
	require.Error(t, err)
}

func TestWorkspaceRuntime_CopyFileOut_SkipsDirHeader(t *testing.T) {
	// copyFileOut should skip directory headers until a file is seen.
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
			// Build tar: first a directory, then a file entry.
			// Docker API requires X-Docker-Container-Path-Stat header.
			w.Header().Set(
				"X-Docker-Container-Path-Stat",
				b64PathStat+b64PathStat2+b64PathStat3,
			)
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			_ = tw.WriteHeader(&tar.Header{
				Name:     "dir/",
				Mode:     0o755,
				Typeflag: tar.TypeDir,
			})
			data := []byte("abc")
			_ = tw.WriteHeader(&tar.Header{
				Name: "file.txt",
				Mode: 0o644,
				Size: int64(len(data)),
			})
			_, _ = tw.Write(data)
			_ = tw.Close()
			_, _ = w.Write(buf.Bytes())
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()
	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{runContainerBase: testRunBase},
	}
	b, _, mime, err := rt.copyFileOut(
		context.Background(), "/work/file.txt",
	)
	require.NoError(t, err)
	require.Equal(t, "abc", string(b))
	require.NotEmpty(t, mime)
}

func TestWorkspaceRuntime_PutDirectory_TarCopy_Error(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(src, "f.txt"), []byte("v"), 0o644,
	))

	var mkdirOK bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			// mkdir -p dest
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			mkdirOK = true
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodPut &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
			// Simulate copy failure
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()
	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{runContainerBase: testRunBase},
	}
	ws := codeexecutor.Workspace{ID: "w6",
		Path: path.Join(testRunBase, "w6")}
	err := rt.PutDirectory(context.Background(), ws, src, "dst")
	require.Error(t, err)
	require.True(t, mkdirOK)
}

func TestWorkspaceRuntime_StageInputs_ArtifactAndWorkspace(t *testing.T) {
	// Sequence exec IDs for mkdir and cp/ln inside container.
	var execIdx int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			execIdx++
			id := testExec1
			if execIdx > 1 {
				id = testExec2
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + id + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec2+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec2+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodPut &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
			// Accept tar uploads for artifact copy.
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{runContainerBase: testRunBase},
	}

	ws := codeexecutor.Workspace{ID: "wsi",
		Path: path.Join(testRunBase, "wsi")}

	// Prepare artifact service and save one artifact.
	svc := inmemory.NewService()
	ctx := codeexecutor.WithArtifactService(
		context.Background(), svc,
	)
	ctx = codeexecutor.WithArtifactSession(ctx, artifact.SessionInfo{
		AppName: "a", UserID: "u", SessionID: "s",
	})
	_, err := codeexecutor.SaveArtifactHelper(
		ctx, "z.txt", []byte("Z"), "text/plain",
	)
	require.NoError(t, err)

	// Stage artifact file to work/inputs/z.txt
	err = rt.StageInputs(ctx, ws, []codeexecutor.InputSpec{{
		From: "artifact://z.txt",
		To:   path.Join(codeexecutor.DirWork, "inputs", "z.txt"),
		Mode: "copy",
	}})
	require.NoError(t, err)

	// Stage workspace path with link mode; results in an exec cp/ln.
	err = rt.StageInputs(context.Background(), ws,
		[]codeexecutor.InputSpec{{
			From: "workspace://foo.txt",
			To:   path.Join(codeexecutor.DirWork, "inputs", "foo.txt"),
			Mode: "link",
		}})
	require.NoError(t, err)
}

func TestWorkspaceRuntime_Collect_NoMatches_And_CopyError(t *testing.T) {
	// Two phases: first no matches, then copy error on a listed file.
	phase := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			if phase == 0 {
				// no results
				writeHijackStream(t, conn, buf, "", "")
			} else {
				// single file
				writeHijackStream(t, conn, buf,
					"/work/x.txt\n", "")
			}
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
			// For phase 1 we do not reach here; for phase 2 force err.
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}
	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()
	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{runContainerBase: testRunBase},
	}
	ws := codeexecutor.Workspace{ID: "w7", Path: "/work"}

	// Phase 0: no matches
	files, err := rt.Collect(
		context.Background(), ws, []string{"out/*.none"},
	)
	require.NoError(t, err)
	require.Len(t, files, 0)

	// Phase 1: one file listed, but copy fails
	phase = 1
	_, err = rt.Collect(
		context.Background(), ws, []string{"x.txt"},
	)
	require.Error(t, err)
}

func TestWorkspaceRuntime_ExecuteInline(t *testing.T) {
	var execCount int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
			// accept staged file
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			execCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "X", "")
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

	rt := &workspaceRuntime{
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

	// Include a failing spec so that stderr shows error lines.
	res, err = rt.ExecuteInline(
		context.Background(), "execID",
		[]codeexecutor.CodeBlock{{
			Code:     "echo ok",
			Language: "badlang",
		}}, time.Second*3,
	)
	require.NoError(t, err)
	require.Contains(t, res.Stderr, "unsupported language")
}

func TestTarFromFiles(t *testing.T) {
	rc, err := tarFromFiles([]codeexecutor.PutFile{{
		Path:    "p/q.txt",
		Content: []byte("value"),
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

func TestWorkspaceRuntime_RunProgram_TimedOut(t *testing.T) {
	// Delay inspect beyond timeout to trigger TimedOut=true.
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/json"):
			// Sleep longer than the RunProgram timeout.
			time.Sleep(50 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()
	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{runContainerBase: testRunBase},
	}
	ws := codeexecutor.Workspace{ID: "wT",
		Path: path.Join(testRunBase, "wT")}
	res, err := rt.RunProgram(
		context.Background(), ws,
		codeexecutor.RunProgramSpec{
			Cmd:     "bash",
			Args:    []string{"-lc", "true"},
			Timeout: 10 * time.Millisecond,
		},
	)
	require.Error(t, err)
	require.True(t, res.TimedOut)
}

func TestWorkspaceRuntime_RunProgram_NoDupWorkspaceEnv(t *testing.T) {
	var captured []string
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			var payload struct {
				Cmd []string `json:"Cmd"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			captured = payload.Cmd
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "ok", "")
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
	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{
			runHostBase:      t.TempDir(),
			runContainerBase: testRunBase,
		},
	}
	ws := codeexecutor.Workspace{ID: "wE",
		Path: path.Join(testRunBase, "wE")}
	// Provide WORKSPACE_DIR in env; runtime should not duplicate it.
	_, err := rt.RunProgram(
		context.Background(), ws,
		codeexecutor.RunProgramSpec{
			Cmd:  "bash",
			Args: []string{"-lc", "true"},
			Env: map[string]string{
				codeexecutor.WorkspaceEnvDirKey: ws.Path,
			},
			Timeout: time.Duration(waitShortSec) * time.Second,
		},
	)
	require.NoError(t, err)
	joined := strings.Join(captured, " ")
	// Count occurrences of WORKSPACE_DIR= only once.
	cnt := strings.Count(
		joined, codeexecutor.WorkspaceEnvDirKey+"=",
	)
	require.Equal(t, 1, cnt)
}

func TestHelpers_Simple(t *testing.T) {
	tmp := t.TempDir()
	b := []string{tmp + ":/opt/trpc-agent/skills:ro"}
	got := findBindSource(b, "/opt/trpc-agent/skills")
	require.Equal(t, tmp, got)

	require.Equal(t, "", findBindSource(b, "/none"))
	require.Equal(t, "ab_12__X", sanitize("ab 12!@X"))

	// shellQuote basics
	require.Equal(t, "''", shellQuote(""))
	sq := shellQuote("a'b")
	require.True(t, strings.HasPrefix(sq, "'"))
	require.True(t, strings.HasSuffix(sq, "'"))
	require.Contains(t, sq, "\\'")

	require.Equal(t, "c", inputBase("a/b/c"))
	require.Equal(t, "abc", inputBase("abc"))
}

func TestTarFromFiles_InvalidPath(t *testing.T) {
	// Paths like "." are rejected by tarFromFiles.
	_, err := tarFromFiles([]codeexecutor.PutFile{{
		Path:    ".",
		Content: []byte("x"),
		Mode:    0o644,
	}})
	require.Error(t, err)
}

// A minimal artifact service for tests.
type artMem struct{ saved int }

func (m *artMem) SaveArtifact(
	_ context.Context,
	_ artifact.SessionInfo,
	_ string,
	_ *artifact.Artifact,
) (int, error) {
	m.saved++
	return m.saved, nil
}

func (*artMem) LoadArtifact(
	_ context.Context,
	_ artifact.SessionInfo,
	_ string,
	_ *int,
) (*artifact.Artifact, error) {
	return &artifact.Artifact{
		Data:     []byte("A1"),
		MimeType: "text/plain",
		Name:     "name",
	}, nil
}

func (*artMem) ListArtifactKeys(
	_ context.Context,
	_ artifact.SessionInfo,
) ([]string, error) {
	return nil, nil
}

func (*artMem) DeleteArtifact(
	_ context.Context,
	_ artifact.SessionInfo,
	_ string,
) error {
	return nil
}

func (*artMem) ListVersions(
	_ context.Context,
	_ artifact.SessionInfo,
	_ string,
) ([]int, error) {
	return nil, nil
}

func TestStageInputs_AllBranches(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodPut &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	inputsRoot := t.TempDir()
	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{
			runContainerBase:    testRunBase,
			inputsHostBase:      inputsRoot,
			inputsContainerBase: "/opt/trpc-agent/inputs",
		},
	}
	ws := codeexecutor.Workspace{ID: "wSI",
		Path: path.Join(testRunBase, "wSI")}

	svc := &artMem{}
	ctx := codeexecutor.WithArtifactService(
		context.Background(), svc,
	)

	specs := []codeexecutor.InputSpec{
		{From: "artifact://name@1", To: "work/a.txt",
			Mode: "copy"},
		{From: "host://" + filepath.Join(inputsRoot, "d1"),
			To: "work/host1", Mode: "link"},
		{From: "host://" + filepath.Join(inputsRoot, "d2"),
			To: "work/host2", Mode: "copy"},
		{From: "workspace://sub", To: "work/ws1",
			Mode: "copy"},
		{From: "skill://tool", To: "work/sk1", Mode: "link"},
	}

	require.NoError(t, rt.StageInputs(ctx, ws, specs))
}

func TestCollectOutputs_SaveInlineLimits(t *testing.T) {
	// Exec lists two files; archive serves content.
	calls := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + testExec1 + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf,
				"/ws/out/a.txt\n/ws/out/b.txt\n", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
			// Serve a tar stream for each file.
			w.Header().Set(
				"X-Docker-Container-Path-Stat",
				b64PathStat+b64PathStat2+b64PathStat3,
			)
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			payload := strings.Repeat("Z", 10)
			_ = tw.WriteHeader(&tar.Header{
				Name: "file",
				Mode: 0o644,
				Size: int64(len(payload)),
			})
			_, _ = tw.Write([]byte(payload))
			_ = tw.Close()
			_, _ = w.Write(buf.Bytes())
			calls++
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}
	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	svc := &artMem{}
	ctx := codeexecutor.WithArtifactService(
		context.Background(), svc,
	)

	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{runContainerBase: testRunBase},
	}
	ws := codeexecutor.Workspace{ID: "wCO", Path: "/ws"}
	mf, err := rt.CollectOutputs(ctx, ws, codeexecutor.OutputSpec{
		Globs:         []string{"out/*.txt"},
		MaxFiles:      1,
		MaxFileBytes:  4,
		MaxTotalBytes: 8,
		Inline:        true,
		Save:          true,
		NameTemplate:  "prefix-",
	})
	require.NoError(t, err)
	require.True(t, mf.LimitsHit)
	require.Len(t, mf.Files, 1)
	require.Equal(t, "out/a.txt", mf.Files[0].Name)
	require.Equal(t, "prefix-out/a.txt", mf.Files[0].SavedAs)
	require.NotZero(t, mf.Files[0].Version)
	require.NotEmpty(t, mf.Files[0].MIMEType)
	require.Equal(t, 4, len(mf.Files[0].Content))
	require.Equal(t, 1, calls)
}

func TestWorkspaceRuntime_StageDirectory_FallbackTarCopy_ReadOnly(t *testing.T) {
	// When AllowMount is false, StageDirectory should fall back to
	// PutDirectory (tar copy) and then apply chmod when ReadOnly is set.
	var execCreates int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			execCreates++
			id := testExec1
			if execCreates > 1 {
				id = testExec2
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + id + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec2+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec2+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodPut &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
			// Accept tar upload for PutDirectory fallback.
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{
			runContainerBase: testRunBase,
		},
	}
	ws := codeexecutor.Workspace{ID: "wSD1",
		Path: path.Join(testRunBase, "wSD1")}

	// Prepare a host directory to copy.
	src := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(src, "f.txt"), []byte("v"), 0o644,
	))

	err := rt.StageDirectory(
		context.Background(), ws, src, "dst",
		codeexecutor.StageOptions{ReadOnly: true, AllowMount: false},
	)
	require.NoError(t, err)
	// Two exec create calls: mkdir (from PutDirectory) and chmod.
	require.Equal(t, 2, execCreates)
}

func TestWorkspaceRuntime_StageDirectory_Fallback_ReadOnly_ChmodError(
	t *testing.T,
) {
	// Simulate an error on the chmod exec (inspect failure).
	var execCreates int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/exec"):
			execCreates++
			id := testExec1
			if execCreates > 1 {
				id = testExec2
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"` + id + `"}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec1+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec2+"/start"):
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			writeHijackStream(t, conn, buf, "", "")
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path,
				"/exec/"+testExec2+"/json"):
			// Return non-OK to force inspect error.
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodPut &&
			strings.Contains(r.URL.Path,
				"/containers/"+testCID+"/archive"):
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}

	cli, cleanup := fakeDocker(t, handler)
	defer cleanup()

	rt := &workspaceRuntime{
		ce: &CodeExecutor{
			client:    cli,
			container: &tcontainer.Summary{ID: testCID},
		},
		cfg: runtimeConfig{runContainerBase: testRunBase},
	}
	ws := codeexecutor.Workspace{ID: "wSD2",
		Path: path.Join(testRunBase, "wSD2")}
	src := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(src, "f.txt"), []byte("v"), 0o644,
	))

	err := rt.StageDirectory(
		context.Background(), ws, src, "dst",
		codeexecutor.StageOptions{ReadOnly: true, AllowMount: false},
	)
	require.Error(t, err)
}
