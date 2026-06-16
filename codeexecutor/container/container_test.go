//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package container

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api"
	tcontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func dockerHost() (string, error) {
	// Check if docker command exists
	cmd := exec.Command("docker", "--version")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("Docker command not found. Please install Docker.")
	}

	// Check if Docker daemon is running by using docker info
	// This will work regardless of the socket path as docker will use the correct one
	cmd = exec.Command("docker", "info")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("Docker daemon is not running or not accessible. Please start Docker daemon.")
	}

	if host := os.Getenv("DOCKER_HOST"); host != "" {
		return host, nil
	}
	cmd = exec.Command("docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get docker context: %v", err)
	}

	host := strings.TrimSpace(string(output))
	if host == "" {
		return "unix:///var/run/docker.sock", nil
	}
	return host, nil
}

func newFakeDockerClient(t *testing.T, handler http.HandlerFunc) (*client.Client, func()) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(handler))

	parsed, err := url.Parse(server.URL)
	if !assert.NoError(t, err) {
		server.Close()
		return nil, func() {}
	}

	cli, err := client.NewClientWithOpts(
		client.WithHost(fmt.Sprintf("tcp://%s", parsed.Host)),
		client.WithVersion(api.DefaultVersion),
	)
	if !assert.NoError(t, err) {
		server.Close()
		return nil, func() {}
	}

	cleanup := func() {
		if cli != nil {
			assert.NoError(t, cli.Close())
		}
		server.Close()
	}

	return cli, cleanup
}

func writeHijackStream(t *testing.T, conn net.Conn, buf *bufio.ReadWriter, stdout, stderr string) {
	t.Helper()

	_, err := buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Type: application/vnd.docker.raw-stream\r\n\r\n")
	assert.NoError(t, err)

	if stdout != "" {
		writeDockerFrame(t, buf, 1, stdout)
	}
	if stderr != "" {
		writeDockerFrame(t, buf, 2, stderr)
	}
	assert.NoError(t, buf.Flush())

	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		assert.NoError(t, closer.CloseWrite())
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = conn.Close()
	}()
}

func writeDockerFrame(t *testing.T, w io.Writer, streamType byte, data string) {
	t.Helper()

	header := make([]byte, 8)
	header[0] = streamType
	binary.BigEndian.PutUint32(header[4:], uint32(len(data)))

	_, err := w.Write(header)
	assert.NoError(t, err)

	if data != "" {
		_, err = w.Write([]byte(data))
		assert.NoError(t, err)
	}
}

func TestCodeExecutor_ExecuteCodeWithoutContainer(t *testing.T) {
	exec := &CodeExecutor{}
	_, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{})
	assert.Error(t, err)
}

func TestCreateBuildContext(t *testing.T) {
	tempDir := t.TempDir()
	targetFile := filepath.Join(tempDir, "sample.txt")
	assert.NoError(t, os.WriteFile(targetFile, []byte("payload"), 0o644))

	reader, err := createBuildContext(tempDir)
	assert.NoError(t, err)
	defer reader.Close()

	tarReader := tar.NewReader(reader)
	found := false
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		assert.NoError(t, err)

		if header.Name == "sample.txt" {
			data, err := io.ReadAll(tarReader)
			assert.NoError(t, err)
			assert.Equal(t, "payload", string(data))
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestGenerateContainerName(t *testing.T) {
	name := generateContainerName()
	assert.True(t, strings.HasPrefix(name, defaultContainerNamePrefix))
	assert.Greater(t, len(name), len(defaultContainerNamePrefix))
}

func TestNew_WithMissingImageAndDockerfile(t *testing.T) {
	_, err := New(WithContainerConfig(tcontainer.Config{}))
	assert.Error(t, err)
}

func TestNew_WithDockerFilePathSetsAbsolute(t *testing.T) {
	tempDir := t.TempDir()

	_, err := New(
		WithDockerFilePath(tempDir),
		WithHost("invalid-host"),
	)
	assert.Error(t, err)
}

func TestNew_DefaultHostInitError(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")

	_, err := New(
		WithDockerFilePath(tempDir),
	)
	assert.Error(t, err)
}

func TestNew_FilepathAbsError(t *testing.T) {
	original, err := os.Getwd()
	assert.NoError(t, err)

	tempDir := t.TempDir()
	assert.NoError(t, os.Chdir(tempDir))
	assert.NoError(t, os.RemoveAll(tempDir))

	t.Cleanup(func() {
		assert.NoError(t, os.Chdir(original))
	})

	_, err = New(WithDockerFilePath("Dockerfile"))
	assert.Error(t, err)
}

// Fake runtime validation via wrapper methods.
func TestCodeExecutor_WrapperMethods_Basic(t *testing.T) {
	c := &CodeExecutor{}

	// Engine returns a non-nil engine even when runtime init fails.
	eng := c.Engine()
	require.NotNil(t, eng)

	// ensureWS caches instance.
	rt1, err := c.ensureWS()
	require.NoError(t, err)
	rt2, err := c.ensureWS()
	require.NoError(t, err)
	require.Equal(t, rt1, rt2)

	// Workspace wrappers should propagate errors when executor not ready.
	ws, err := c.CreateWorkspace(
		context.Background(), "id", codeexecutor.WorkspacePolicy{},
	)
	require.Error(t, err)
	require.Empty(t, ws.Path)

	// Cleanup on empty path is a no-op.
	err = c.Cleanup(context.Background(), codeexecutor.Workspace{})
	require.NoError(t, err)

	// PutFiles with empty slice is a no-op.
	err = c.PutFiles(context.Background(), codeexecutor.Workspace{}, nil)
	require.NoError(t, err)

	// Avoid calling Collect/RunProgram with nil executor to prevent
	// lower-level panics in exec paths.
}

func TestCodeBlockDelimiter_Value(t *testing.T) {
	c := &CodeExecutor{}
	d := c.CodeBlockDelimiter()
	require.Equal(t, "```", d.Start)
	require.Equal(t, "```", d.End)
}

func TestCodeExecutor_Close_NoClient(t *testing.T) {
	c := &CodeExecutor{}
	require.NoError(t, c.Close())
}

func TestNew_Success(t *testing.T) {
	const execID = "exec-new"
	var inspectCalls int

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == "/_ping":
			w.Header().Set("API-Version", api.DefaultVersion)
			_, _ = w.Write([]byte("OK"))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"RepoTags":["python:3.9-slim"]}]`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"cid","Warnings":[]}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/start"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/cid/json"):
			inspectCalls++
			w.Header().Set("Content-Type", "application/json")
			if inspectCalls == 1 {
				_, _ = w.Write([]byte(`{"State":{"Running":true,"Status":"running","ExitCode":0}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ID":"cid","Name":"/test","Image":"python:3.9-slim","State":{"Running":true,"Status":"running","ExitCode":0}}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"Id":"%s"}`, execID)))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/exec/"+execID+"/start"):
			hj, ok := w.(http.Hijacker)
			assert.True(t, ok)
			conn, buffer, err := hj.Hijack()
			assert.NoError(t, err)
			writeHijackStream(t, conn, buffer, "", "")
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/exec/"+execID+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/stop"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/containers/cid"):
			w.WriteHeader(http.StatusNoContent)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	assert.NoError(t, err)
	host := fmt.Sprintf("tcp://%s", parsed.Host)

	exec, err := New(
		WithHost(host),
		WithContainerConfig(tcontainer.Config{
			Image: "python:3.9-slim",
			Cmd:   []string{"tail", "-f", "/dev/null"},
		}),
	)
	assert.NoError(t, err)
	assert.NotNil(t, exec)
	assert.NotNil(t, exec.container)
	assert.Equal(t, "python:3.9-slim", exec.container.Image)
	assert.Equal(t, "running", exec.container.State)
	assert.NoError(t, exec.Close())
}

func TestWithHostConfigOption(t *testing.T) {
	optionCfg := tcontainer.HostConfig{AutoRemove: false, NetworkMode: "bridge"}
	exec := &CodeExecutor{}

	WithHostConfig(optionCfg)(exec)
	assert.Equal(t, optionCfg.AutoRemove, exec.hostConfig.AutoRemove)
	assert.Equal(t, optionCfg.NetworkMode, exec.hostConfig.NetworkMode)
}

func TestWithBindMountOption(t *testing.T) {
	exec := &CodeExecutor{}
	WithBindMount("/host/src", "/ctr/dst", "ro")(exec)
	assert.Len(t, exec.hostConfig.Binds, 1)
	assert.Equal(t, "/host/src:/ctr/dst:ro", exec.hostConfig.Binds[0])

	// No mode provided should omit the trailing colon.
	WithBindMount("/a", "/b", "")(exec)
	assert.Equal(t, "/a:/b", exec.hostConfig.Binds[1])
}

func TestEnsureWS_CachesRuntime(t *testing.T) {
	c := &CodeExecutor{}
	rt1, err := c.ensureWS()
	assert.NoError(t, err)
	assert.NotNil(t, rt1)
	rt2, err := c.ensureWS()
	assert.NoError(t, err)
	assert.Same(t, rt1, rt2)
}

func TestEngine_NonNil(t *testing.T) {
	c := &CodeExecutor{}
	eng := c.Engine()
	assert.NotNil(t, eng)
}

func TestWorkspaceWrappers_MinimalPaths(t *testing.T) {
	c := &CodeExecutor{}
	ctx := context.Background()

	// CreateWorkspace should fail when executor is not ready.
	_, err := c.CreateWorkspace(ctx, "id", codeexecutor.WorkspacePolicy{})
	assert.Error(t, err)

	// Cleanup with empty path should be a no-op.
	err = c.Cleanup(ctx, codeexecutor.Workspace{})
	assert.NoError(t, err)

	// PutFiles with no files should be a no-op.
	err = c.PutFiles(ctx, codeexecutor.Workspace{}, nil)
	assert.NoError(t, err)

	// PutDirectory with empty hostPath should error out.
	err = c.PutDirectory(ctx, codeexecutor.Workspace{}, "", "to")
	assert.Error(t, err)

	// ExecuteInline should fail when executor is not ready.
	_, err = c.ExecuteInline(ctx, "id", nil, time.Second)
	assert.Error(t, err)
}

func TestWrappers_RunProgram_And_Collect(t *testing.T) {
	const execID = "exec-wrap"
	var startCalls int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/containers/cid/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				fmt.Sprintf(`{"Id":"%s"}`, execID),
			))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/exec/"+execID+"/start"):
			hj, ok := w.(http.Hijacker)
			assert.True(t, ok)
			conn, buf, err := hj.Hijack()
			assert.NoError(t, err)
			if startCalls == 0 {
				writeHijackStream(t, conn, buf, "ok\n", "")
			} else {
				writeHijackStream(t, conn, buf, "", "")
			}
			startCalls++
		case r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "/exec/"+execID+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}
	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	c := &CodeExecutor{
		client:    cli,
		container: &tcontainer.Summary{ID: "cid"},
	}
	ws := codeexecutor.Workspace{ID: "id", Path: "/work"}
	ctx := context.Background()

	// RunProgram wrapper
	res, err := c.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     "bash",
		Args:    []string{"-lc", "echo ok"},
		Timeout: time.Second,
	})
	assert.NoError(t, err)
	assert.Contains(t, res.Stdout, "ok")

	// Collect with no patterns should return empty without copy out.
	files, err := c.Collect(ctx, ws, nil)
	assert.NoError(t, err)
	assert.Empty(t, files)
}

func TestWithContainerNameOption(t *testing.T) {
	exec := &CodeExecutor{}
	WithContainerName("custom")(exec)
	assert.Equal(t, "custom", exec.containerName)
}

func TestEnsureImageExists_ImagePresent(t *testing.T) {
	targetImage := "example/image:latest"
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/images/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`[{"RepoTags":["%s"]}]`, targetImage)))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		containerConfig: tcontainer.Config{
			Image: targetImage,
		},
	}

	assert.NoError(t, exec.ensureImageExists(context.Background()))
}

func TestEnsureImageExists_PullImage(t *testing.T) {
	targetImage := "example/image:latest"
	var pulled atomic.Bool

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/images/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/images/create"):
			pulled.Store(true)
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(`{"status":"pulling"}`))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		containerConfig: tcontainer.Config{
			Image: targetImage,
		},
	}

	assert.NoError(t, exec.ensureImageExists(context.Background()))
	assert.True(t, pulled.Load(), "expected image to be pulled")
}

func TestEnsureImageExists_PullError(t *testing.T) {
	targetImage := "example/image:latest"
	var pullAttempt bool

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/images/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/images/create"):
			pullAttempt = true
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`error`))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		containerConfig: tcontainer.Config{
			Image: targetImage,
		},
	}

	err := exec.ensureImageExists(context.Background())
	assert.Error(t, err)
	assert.True(t, pullAttempt)
}

func TestEnsureImageExists_ListError(t *testing.T) {
	var listCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/json"):
			listCalled = true
			http.Error(w, "list fail", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		containerConfig: tcontainer.Config{
			Image: "example/image:latest",
		},
	}

	err := exec.ensureImageExists(context.Background())
	assert.Error(t, err)
	assert.True(t, listCalled)
}

func TestEnsureImageExists_ReadPullError(t *testing.T) {
	var pullCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/images/create"):
			pullCalled = true
			w.Header().Set("Content-Length", "10")
			_, _ = w.Write([]byte("12345"))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		containerConfig: tcontainer.Config{
			Image: "example/image:latest",
		},
	}

	err := exec.ensureImageExists(context.Background())
	assert.Error(t, err)
	assert.True(t, pullCalled)
}

func TestExecuteCode_UnsupportedLanguage(t *testing.T) {
	exec := &CodeExecutor{
		container: &tcontainer.Summary{ID: "cid"},
	}

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Code: "puts 'hi'", Language: "ruby"},
		},
	}

	result, err := exec.ExecuteCode(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result.Output, "unsupported language: ruby")
}

func TestExecuteCode_BashExecCreateError(t *testing.T) {
	var execCreateCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/exec"):
			execCreateCalled = true
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client:    cli,
		container: &tcontainer.Summary{ID: "cid"},
	}

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Code: "echo hi", Language: "bash"},
		},
	}

	_, err := exec.ExecuteCode(context.Background(), input)
	assert.Error(t, err)
	assert.True(t, execCreateCalled)
}

func TestExecuteCode_AttachError(t *testing.T) {
	const execID = "exec-1"
	var execStartCalled bool

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"Id":"%s"}`, execID)))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/exec/"+execID+"/start"):
			execStartCalled = true
			w.WriteHeader(http.StatusOK)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client:    cli,
		container: &tcontainer.Summary{ID: "cid"},
	}

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Code: "print('test')", Language: "python"},
		},
	}

	_, err := exec.ExecuteCode(context.Background(), input)
	assert.Error(t, err)
	assert.True(t, execStartCalled)
}

func TestExecuteCode_Success(t *testing.T) {
	type execData struct {
		stdout string
		stderr string
	}

	outputs := []execData{
		{stdout: "python output\n"},
		{stdout: "bash output\n", stderr: "bash warn\n"},
	}

	var (
		mu         sync.Mutex
		callIndex  int
		outputByID = make(map[string]execData)
	)

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/exec"):
			mu.Lock()
			defer mu.Unlock()
			if callIndex >= len(outputs) {
				assert.Failf(t, "unexpected exec create call", "%d", callIndex)
				return
			}
			execID := fmt.Sprintf("exec-%d", callIndex)
			outputByID[execID] = outputs[callIndex]
			callIndex++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"Id":"%s"}`, execID)))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/exec/"):
			mu.Lock()
			defer mu.Unlock()
			for execID, data := range outputByID {
				if strings.Contains(r.URL.Path, "/exec/"+execID+"/start") {
					hj, ok := w.(http.Hijacker)
					assert.True(t, ok)
					conn, buffer, err := hj.Hijack()
					assert.NoError(t, err)
					writeHijackStream(t, conn, buffer, data.stdout, data.stderr)
					delete(outputByID, execID)
					return
				}
			}
			assert.Failf(t, "unexpected exec start path", "%s", r.URL.Path)
			return
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client:    cli,
		container: &tcontainer.Summary{ID: "cid"},
	}

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Code: "print('python')", Language: "python"},
			{Code: "echo 'bash'", Language: "bash"},
		},
	}

	result, err := exec.ExecuteCode(context.Background(), input)
	assert.NoError(t, err)
	assert.Contains(t, result.Output, "python output\n")
	assert.Contains(t, result.Output, "bash output\n")
	assert.Contains(t, result.Output, "bash warn\n")
}

func TestVerifyPythonInstallation_CreateError(t *testing.T) {
	var execCreateCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/exec"):
			execCreateCalled = true
			http.Error(w, "failed", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client:    cli,
		container: &tcontainer.Summary{ID: "cid"},
	}

	err := exec.verifyPythonInstallation(context.Background())
	assert.Error(t, err)
	assert.True(t, execCreateCalled)
}

func TestVerifyPythonInstallation_AttachError(t *testing.T) {
	const execID = "exec-verify"
	var execStartCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"Id":"%s"}`, execID)))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/exec/"+execID+"/start"):
			execStartCalled = true
			w.WriteHeader(http.StatusOK)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client:    cli,
		container: &tcontainer.Summary{ID: "cid"},
	}

	err := exec.verifyPythonInstallation(context.Background())
	assert.Error(t, err)
	assert.True(t, execStartCalled)
}

func TestVerifyPythonInstallation_NotInstalled(t *testing.T) {
	const execID = "exec-verify"
	var inspectCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"Id":"%s"}`, execID)))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/exec/"+execID+"/start"):
			hj, ok := w.(http.Hijacker)
			assert.True(t, ok)
			conn, buffer, err := hj.Hijack()
			assert.NoError(t, err)
			writeHijackStream(t, conn, buffer, "", "")
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/exec/"+execID+"/json"):
			inspectCalled = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":1}`))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client:    cli,
		container: &tcontainer.Summary{ID: "cid"},
	}

	err := exec.verifyPythonInstallation(context.Background())
	assert.Error(t, err)
	assert.True(t, inspectCalled)
}

func TestVerifyPythonInstallation_Success(t *testing.T) {
	const execID = "exec-verify"
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"Id":"%s"}`, execID)))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/exec/"+execID+"/start"):
			hj, ok := w.(http.Hijacker)
			assert.True(t, ok)
			conn, buffer, err := hj.Hijack()
			assert.NoError(t, err)
			writeHijackStream(t, conn, buffer, "", "")
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/exec/"+execID+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client:    cli,
		container: &tcontainer.Summary{ID: "cid"},
	}

	err := exec.verifyPythonInstallation(context.Background())
	assert.NoError(t, err)
}

func TestVerifyPythonInstallation_InspectError(t *testing.T) {
	const execID = "exec-verify"
	var inspectCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"Id":"%s"}`, execID)))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/exec/"+execID+"/start"):
			hj, ok := w.(http.Hijacker)
			assert.True(t, ok)
			conn, buffer, err := hj.Hijack()
			assert.NoError(t, err)
			writeHijackStream(t, conn, buffer, "", "")
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/exec/"+execID+"/json"):
			inspectCalled = true
			http.Error(w, "inspect error", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client:    cli,
		container: &tcontainer.Summary{ID: "cid"},
	}

	err := exec.verifyPythonInstallation(context.Background())
	assert.Error(t, err)
	assert.True(t, inspectCalled)
}

func TestBuildDockerImage_Success(t *testing.T) {
	var received bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/build"):
			_, err := io.Copy(io.Discard, r.Body)
			assert.NoError(t, err)
			received = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"stream":"done"}`))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	tempDir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(tempDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))

	exec := &CodeExecutor{
		client:         cli,
		dockerFilePath: tempDir,
		containerConfig: tcontainer.Config{
			Image: "example/image:latest",
		},
	}

	assert.NoError(t, exec.buildDockerImage(context.Background()))
	assert.True(t, received)
}

func TestBuildDockerImage_Error(t *testing.T) {
	var buildCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/build"):
			buildCalled = true
			http.Error(w, "fail", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	tempDir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(tempDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))

	exec := &CodeExecutor{
		client:         cli,
		dockerFilePath: tempDir,
		containerConfig: tcontainer.Config{
			Image: "example/image:latest",
		},
	}

	err := exec.buildDockerImage(context.Background())
	assert.Error(t, err)
	assert.True(t, buildCalled)
}

func TestBuildDockerImage_ReadOutputError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/build"):
			w.Header().Set("Content-Length", "10")
			_, _ = w.Write([]byte("12345"))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	tempDir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(tempDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))

	exec := &CodeExecutor{
		client:         cli,
		dockerFilePath: tempDir,
		containerConfig: tcontainer.Config{
			Image: "example/image:latest",
		},
	}

	assert.NoError(t, exec.buildDockerImage(context.Background()))
}

func TestInitContainer_CreateError(t *testing.T) {
	var createCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"RepoTags":["python:3.9-slim"]}]`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			createCalled = true
			http.Error(w, "create fail", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		containerConfig: tcontainer.Config{
			Image: "python:3.9-slim",
		},
		hostConfig:    tcontainer.HostConfig{},
		containerName: "test",
	}

	err := exec.initContainer()
	assert.Error(t, err)
	assert.True(t, createCalled)
}

func TestInitContainer_StartError(t *testing.T) {
	var startCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"RepoTags":["python:3.9-slim"]}]`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"cid","Warnings":[]}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/start"):
			startCalled = true
			http.Error(w, "start fail", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		containerConfig: tcontainer.Config{
			Image: "python:3.9-slim",
		},
		hostConfig:    tcontainer.HostConfig{},
		containerName: "test",
	}

	err := exec.initContainer()
	assert.Error(t, err)
	assert.True(t, startCalled)
}

func TestInitContainer_WaitError(t *testing.T) {
	var inspectCalls int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"RepoTags":["python:3.9-slim"]}]`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"cid","Warnings":[]}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/start"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/cid/json"):
			inspectCalls++
			if inspectCalls == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"State":{"Running":false,"Status":"exited","ExitCode":2}}`))
				return
			}
			assert.Failf(t, "unexpected inspect call", "%d", inspectCalls)
			return
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		containerConfig: tcontainer.Config{
			Image: "python:3.9-slim",
		},
		hostConfig:    tcontainer.HostConfig{},
		containerName: "test",
	}

	err := exec.initContainer()
	assert.Error(t, err)
	assert.Equal(t, 1, inspectCalls)
}

func TestInitContainer_InspectError(t *testing.T) {
	var inspectCalls int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"RepoTags":["python:3.9-slim"]}]`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"cid","Warnings":[]}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/start"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/cid/json"):
			inspectCalls++
			if inspectCalls == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"State":{"Running":true,"Status":"running","ExitCode":0}}`))
				return
			}
			http.Error(w, "inspect fail", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		containerConfig: tcontainer.Config{
			Image: "python:3.9-slim",
		},
		hostConfig:    tcontainer.HostConfig{},
		containerName: "test",
	}

	err := exec.initContainer()
	assert.Error(t, err)
	assert.Equal(t, 2, inspectCalls)
}

func TestInitContainer_NotRunning(t *testing.T) {
	var inspectCalls int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"RepoTags":["python:3.9-slim"]}]`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"cid","Warnings":[]}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/start"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/cid/json"):
			inspectCalls++
			if inspectCalls == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"State":{"Running":true,"Status":"running","ExitCode":0}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"State":{"Running":false,"Status":"created","ExitCode":0}}`))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		containerConfig: tcontainer.Config{
			Image: "python:3.9-slim",
		},
		hostConfig:    tcontainer.HostConfig{},
		containerName: "test",
	}

	err := exec.initContainer()
	assert.Error(t, err)
	assert.Equal(t, 2, inspectCalls)
}

func TestInitContainer_VerifyError(t *testing.T) {
	var inspectCalls int
	var execCreateCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"RepoTags":["python:3.9-slim"]}]`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"cid","Warnings":[]}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/start"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/cid/json"):
			inspectCalls++
			w.Header().Set("Content-Type", "application/json")
			if inspectCalls == 1 {
				_, _ = w.Write([]byte(`{"State":{"Running":true,"Status":"running","ExitCode":0}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ID":"cid","Name":"/test","Image":"python:3.9-slim","State":{"Running":true,"Status":"running","ExitCode":0}}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/exec"):
			execCreateCalled = true
			http.Error(w, "exec fail", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		containerConfig: tcontainer.Config{
			Image: "python:3.9-slim",
		},
		hostConfig:    tcontainer.HostConfig{},
		containerName: "test",
	}

	err := exec.initContainer()
	assert.Error(t, err)
	assert.Equal(t, 2, inspectCalls)
	assert.True(t, execCreateCalled)
}

func TestInitContainer_Success(t *testing.T) {
	const execID = "exec-success"
	var inspectCalls int

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"RepoTags":["python:3.9-slim"]}]`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"cid","Warnings":[]}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/start"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/cid/json"):
			inspectCalls++
			w.Header().Set("Content-Type", "application/json")
			if inspectCalls == 1 {
				_, _ = w.Write([]byte(`{"State":{"Running":true,"Status":"running","ExitCode":0}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ID":"cid","Name":"/test","Image":"python:3.9-slim","State":{"Running":true,"Status":"running","ExitCode":0}}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/cid/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"Id":"%s"}`, execID)))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/exec/"+execID+"/start"):
			hj, ok := w.(http.Hijacker)
			assert.True(t, ok)
			conn, buffer, err := hj.Hijack()
			assert.NoError(t, err)
			writeHijackStream(t, conn, buffer, "", "")
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/exec/"+execID+"/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ExitCode":0}`))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		containerConfig: tcontainer.Config{
			Image: "python:3.9-slim",
		},
		hostConfig:    tcontainer.HostConfig{},
		containerName: "test",
	}

	assert.NoError(t, exec.initContainer())
	assert.NotNil(t, exec.container)
	assert.Equal(t, "cid", exec.container.ID)
}

func TestInitContainer_EnsureImageError(t *testing.T) {
	var listCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/json"):
			listCalled = true
			http.Error(w, "list fail", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client:         cli,
		dockerFilePath: "",
		containerConfig: tcontainer.Config{
			Image: "python:3.9-slim",
		},
		hostConfig:    tcontainer.HostConfig{},
		containerName: "test",
	}

	err := exec.initContainer()
	assert.Error(t, err)
	assert.True(t, listCalled)
}

func TestInitContainer_BuildError(t *testing.T) {
	var buildCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/build"):
			buildCalled = true
			http.Error(w, "build fail", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client:         cli,
		dockerFilePath: t.TempDir(),
		containerConfig: tcontainer.Config{
			Image: "example/image:latest",
		},
		hostConfig:    tcontainer.HostConfig{},
		containerName: "test",
	}

	// Ensure Dockerfile exists to avoid context error.
	assert.NoError(t, os.WriteFile(filepath.Join(exec.dockerFilePath, "Dockerfile"), []byte("FROM scratch\n"), 0o644))

	err := exec.initContainer()
	assert.Error(t, err)
	assert.True(t, buildCalled)
}

func TestCleanup_NoResources(t *testing.T) {
	exec := &CodeExecutor{}
	exec.cleanup()
}

func TestClose_NoClient(t *testing.T) {
	exec := &CodeExecutor{}
	assert.NoError(t, exec.Close())
}

func TestClose_WithClient(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
		return
	}
	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
	}
	assert.NoError(t, exec.Close())
}

func TestCleanup_StopRemoveError(t *testing.T) {
	stopPath := "/containers/problem/stop"
	removePath := "/containers/problem"
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, stopPath):
			http.Error(w, "stop fail", http.StatusInternalServerError)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, removePath):
			http.Error(w, "remove fail", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client:    cli,
		container: &tcontainer.Summary{ID: "problem"},
	}

	exec.cleanup()
}

func TestInitContainerWithoutClient(t *testing.T) {
	exec := &CodeExecutor{}
	err := exec.initContainer()
	assert.Error(t, err)
}

func TestWaitForContainerReady_Succeeds(t *testing.T) {
	var mu sync.Mutex
	callCount := 0

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/test-container/json"):
			mu.Lock()
			callCount++
			current := callCount
			mu.Unlock()

			running := current >= 2
			status := "created"
			if running {
				status = "running"
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"State":{"Running":%t,"Status":"%s","ExitCode":0}}`, running, status)))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{client: cli}
	ctx := context.Background()
	assert.NoError(t, exec.waitForContainerReady(ctx, time.Second, "test-container"))
}

func TestWaitForContainerReady_Exited(t *testing.T) {
	var inspectCalled bool
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/failing/json"):
			inspectCalled = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"State":{"Running":false,"Status":"exited","ExitCode":2}}`))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{client: cli}
	ctx := context.Background()
	err := exec.waitForContainerReady(ctx, time.Second, "failing")
	assert.Error(t, err)
	assert.True(t, inspectCalled)
}

func TestWaitForContainerReady_InspectError(t *testing.T) {
	containerID := "aaaaaaaaaaaa"
	var inspectCalled bool

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasSuffix(r.URL.Path, "_ping"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/"+containerID+"/json"):
			inspectCalled = true
			http.Error(w, "inspect error", http.StatusInternalServerError)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	assert.NoError(t, err)

	cli, err := client.NewClientWithOpts(
		client.WithHost(fmt.Sprintf("tcp://%s", parsed.Host)),
		client.WithVersion(api.DefaultVersion),
	)
	assert.NoError(t, err)
	t.Cleanup(func() {
		_ = cli.Close()
	})

	exec := &CodeExecutor{client: cli}
	err = exec.waitForContainerReady(context.Background(), 200*time.Millisecond, containerID)
	assert.Error(t, err)
	assert.True(t, inspectCalled)
}

func TestWaitForContainerReady_Timeout(t *testing.T) {
	containerID := "bbbbbbbbbbbb"
	var calls int
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasSuffix(r.URL.Path, "_ping"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/"+containerID+"/json"):
			calls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"State":{"Running":false,"Status":"created","ExitCode":0}}`))
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	assert.NoError(t, err)

	cli, err := client.NewClientWithOpts(
		client.WithHost(fmt.Sprintf("tcp://%s", parsed.Host)),
		client.WithVersion(api.DefaultVersion),
	)
	assert.NoError(t, err)
	t.Cleanup(func() {
		_ = cli.Close()
	})

	exec := &CodeExecutor{client: cli}
	err = exec.waitForContainerReady(context.Background(), 200*time.Millisecond, containerID)
	assert.Error(t, err)
	assert.GreaterOrEqual(t, calls, 1)
}

func TestCleanupStopsAndRemovesContainer(t *testing.T) {
	var mu sync.Mutex
	stopCount := 0
	removeCount := 0

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/test-id/stop"):
			mu.Lock()
			stopCount++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/containers/test-id"):
			mu.Lock()
			removeCount++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
			return
		}
	}

	cli, cleanup := newFakeDockerClient(t, handler)
	defer cleanup()

	exec := &CodeExecutor{
		client: cli,
		container: &tcontainer.Summary{
			ID: "test-id",
		},
	}

	exec.cleanup()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, stopCount)
	assert.Equal(t, 1, removeCount)
}

func TestContainerCodeExecutor_Basic(t *testing.T) {
	host, err := dockerHost()
	if err != nil {
		t.Skipf("Skipping container tests: %s", err)
	}

	// Test with python:3.9-slim image (commonly available)
	executor, err := New(
		WithContainerConfig(tcontainer.Config{
			Image: "python:3.9-slim",
			Cmd:   []string{"tail", "-f", "/dev/null"}, // Keep container running

		}),
		WithHost(host),
	)
	assert.NoError(t, err)
	t.Cleanup(func() {
		executor.Close()
	})

	// Test simple Python code execution
	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{
				Code:     "print('Hello from container!')",
				Language: "python",
			},
		},
		ExecutionID: "test-1",
	}

	result, err := executor.ExecuteCode(context.Background(), input)
	if err != nil {
		assert.NoError(t, err)
	}

	expectedOutput := "Hello from container!\n"
	if result.Output != expectedOutput {
		t.Errorf("Expected output %q, got %q", expectedOutput, result.Output)
	}
}

func TestContainerCodeExecutor_WithOptions(t *testing.T) {
	// Test option functions
	host := "unix:///var/run/docker.sock"
	image := "python:3.8"
	dockerPath := "/tmp/test"

	executor := &CodeExecutor{}

	WithHost(host)(executor)
	if executor.host != host {
		t.Errorf("Expected host %s, got %s", host, executor.host)
	}

	WithContainerConfig(tcontainer.Config{
		Image: image,
	})(executor)
	if executor.containerConfig.Image != image {
		t.Errorf("Expected image %s, got %s", image, executor.containerConfig.Image)
	}

	WithDockerFilePath(dockerPath)(executor)
	// Note: dockerFilePath gets converted to absolute path, so we just check it's not empty
	if executor.dockerFilePath == "" {
		t.Error("Expected dockerFilePath to be set")
	}
}

func TestContainerCodeExecutor_ExecuteCode(t *testing.T) {
	host, err := dockerHost()
	if err != nil {
		t.Skipf("Skipping container tests: %s", err)
	}

	tests := []struct {
		name     string
		input    codeexecutor.CodeExecutionInput
		expected struct {
			outputContains string
			shouldError    bool
		}
	}{
		{
			name: "python hello world",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "print('Hello from Container!')",
						Language: "python",
					},
				},
				ExecutionID: "test-container-python-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "Hello from Container!",
				shouldError:    false,
			},
		},
		{
			name: "bash echo",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "echo 'Hello from Bash Container!'",
						Language: "bash",
					},
				},
				ExecutionID: "test-container-bash-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "Hello from Bash Container!",
				shouldError:    false,
			},
		},
		{
			name: "multiple code blocks",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "echo 'First container block'",
						Language: "bash",
					},
					{
						Code:     "print('Second container block')",
						Language: "python",
					},
				},
				ExecutionID: "test-container-multiple-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "First container block",
				shouldError:    false,
			},
		},
		{
			name: "unsupported language",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "puts 'Hello, Ruby!'",
						Language: "ruby",
					},
				},
				ExecutionID: "test-container-unsupported-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "unsupported language: ruby",
				shouldError:    false,
			},
		},
	}
	executor, err := New(
		WithContainerConfig(tcontainer.Config{
			Image: "python:3.9-slim",
			Cmd:   []string{"tail", "-f", "/dev/null"}, // Keep container running

		}),
		WithHost(host),
	)
	assert.NoError(t, err)
	t.Cleanup(func() {
		executor.Close()
	})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			assert.NoError(t, err)
			ctx := context.Background()

			result, err := executor.ExecuteCode(ctx, tt.input)

			if tt.expected.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Debug output for failed tests
			t.Logf("Test: %s", tt.name)
			t.Logf("Output: %q", result.Output)

			if tt.expected.outputContains != "" {
				assert.Contains(t, result.Output, tt.expected.outputContains,
					"Expected output to contain '%s', but got: '%s'", tt.expected.outputContains, result.Output)
			}

			// OutputFiles should be empty for now
			assert.Empty(t, result.OutputFiles)
		})
	}
}

func TestContainerCodeExecutor_CodeBlockDelimiter(t *testing.T) {
	host, err := dockerHost()
	if err != nil {
		t.Skipf("Skipping container tests: %s", err)

	}
	executor, err := New(
		WithContainerConfig(tcontainer.Config{
			Image: "python:3.9-slim",
			Cmd:   []string{"tail", "-f", "/dev/null"}, // Keep container running

		}),
		WithHost(host),
	)
	assert.NoError(t, err)
	t.Cleanup(func() {
		executor.Close()
	})
	delimiter := executor.CodeBlockDelimiter()

	assert.Equal(t, "```", delimiter.Start)
	assert.Equal(t, "```", delimiter.End)
}

func TestContainerCodeExecutor_IntegrationTest(t *testing.T) {
	host, err := dockerHost()
	if err != nil {
		t.Skipf("Skipping container integration tests: %s", err)
	}

	input := `Let's test container execution with multiple languages:

` + "```python" + `
print("Python in container")
` + "```" + `

` + "```bash" + `
echo "Bash in container"
` + "```"

	// Step 1: Extract code blocks
	delimiter := codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
	blocks := codeexecutor.ExtractCodeBlock(input, delimiter)
	assert.Len(t, blocks, 2)

	// Step 2: Execute in containers
	executor, err := New(
		WithContainerConfig(tcontainer.Config{
			Image: "python:3.9-slim",
			Cmd:   []string{"tail", "-f", "/dev/null"}, // Keep container running
		}),
		WithHost(host),
	)
	assert.NoError(t, err)
	t.Cleanup(func() {
		executor.Close()
	})
	ctx := context.Background()

	executionInput := codeexecutor.CodeExecutionInput{
		CodeBlocks:  blocks,
		ExecutionID: "container-integration-test",
	}

	result, err := executor.ExecuteCode(ctx, executionInput)
	assert.NoError(t, err)

	// Step 3: Format and verify result
	formattedResult := result.String()

	assert.Contains(t, result.Output, "Python in container")
	assert.Contains(t, result.Output, "Bash in container")
	assert.Contains(t, formattedResult, "Code execution result:")

	t.Logf("Container execution result: %s", result.Output)
	t.Logf("Formatted result: %s", formattedResult)
}
