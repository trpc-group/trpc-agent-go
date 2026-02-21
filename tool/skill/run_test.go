//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	testSkillName    = "demo"
	skillFileName    = "SKILL.md"
	timeoutSecSmall  = 5
	outGlobTxt       = "out/*.txt"
	outGlobAll       = "out/*"
	outATxt          = "out/a.txt"
	outAPng          = "out/a.png"
	outBTxt          = "out/b.txt"
	scriptsDir       = "scripts"
	contentHi        = "hi"
	contentHello     = "hello"
	contentMsg       = "msg"
	pngHeaderEscaped = "\\x89PNG\\r\\n\\x1a\\n"
	echoOK           = "echo ok"
	cmdEcho          = "echo"
	cmdLS            = "ls"
	cmdEchoThenLS    = "echo ok; ls"

	errCollectFail     = "collect-fail"
	errPutFail         = "put-fail"
	errRunFail         = "run-fail"
	metadataWhitespace = " "
)

const metadataZeroValuesJSON = `{
  "version": 0,
  "created_at": "0001-01-01T00:00:00Z",
  "skills": null
}`

// writeSkill creates a minimal skill folder.
func writeSkill(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	data := "---\nname: " + name + "\n" +
		"description: test skill\n---\nbody\n"
	err := os.WriteFile(filepath.Join(dir, skillFileName),
		[]byte(data), 0o644)
	require.NoError(t, err)
	return dir
}

func TestRunTool_ExecutesAndCollectsOutputFiles(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	// Use local runtime. No special options needed.
	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt + "; echo ZZZ",
		OutputFiles: []string{outGlobTxt},
		Timeout:     timeoutSecSmall,
	}

	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Contains(t, out.Stdout, "ZZZ")
	require.False(t, out.TimedOut)
	require.NotEmpty(t, out.Duration)

	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, outATxt, out.OutputFiles[0].Name)
	require.Contains(t, out.OutputFiles[0].Content, contentHi)

	require.NotNil(t, out.PrimaryOutput)
	require.Equal(t, outATxt, out.PrimaryOutput.Name)
	require.Contains(t, out.PrimaryOutput.Content, contentHi)
}

func TestRunTool_DoesNotInlineNonTextOutputs(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	cmd := strings.Join([]string{
		"mkdir -p out",
		"printf '" + pngHeaderEscaped + "' > " + outAPng,
		"echo " + contentHi + " > " + outATxt,
	}, "; ")

	args := runInput{
		Skill:       testSkillName,
		Command:     cmd,
		OutputFiles: []string{outGlobAll},
		Timeout:     timeoutSecSmall,
	}

	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.OutputFiles, 2)

	got := make(map[string]runFile, len(out.OutputFiles))
	for _, f := range out.OutputFiles {
		got[f.Name] = f
	}

	png, ok := got[outAPng]
	require.True(t, ok)
	require.Equal(t, "", png.Content)
	require.Equal(t, "image/png", png.MIMEType)
	require.False(t, png.Truncated)
	require.NotZero(t, png.SizeBytes)

	txt, ok := got[outATxt]
	require.True(t, ok)
	require.Contains(t, txt.Content, contentHi)

	require.NotNil(t, out.PrimaryOutput)
	require.Equal(t, outATxt, out.PrimaryOutput.Name)
	require.Contains(t, out.PrimaryOutput.Content, contentHi)
}

func TestRunTool_TrimsTruncatedUTF8TextOutputs(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	const bytes4MiB = 4 * 1024 * 1024
	cmd := strings.Join([]string{
		"set -e",
		"mkdir -p out",
		"head -c " + strconv.Itoa(bytes4MiB-1) +
			" /dev/zero | tr '\\000' 'a' > " + outATxt,
		"printf '\\xE2\\x82\\xAC\\n' >> " + outATxt,
	}, "; ")

	args := runInput{
		Skill:       testSkillName,
		Command:     cmd,
		OutputFiles: []string{outATxt},
		Timeout:     timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(ctx, enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.OutputFiles, 1)

	f := out.OutputFiles[0]
	require.Equal(t, outATxt, f.Name)
	require.True(t, strings.HasPrefix(f.MIMEType, "text/plain"))
	require.True(t, f.Truncated)
	require.Equal(t, bytes4MiB-1, len(f.Content))
	require.True(t, strings.HasSuffix(f.Content, "a"))

	got, _, handled, err := fileref.TryRead(ctx, f.Ref)
	require.True(t, handled)
	require.NoError(t, err)
	require.Equal(t, bytes4MiB-1, len(got))
	require.True(t, strings.HasSuffix(got, "a"))
}

func TestShouldInlineFileContent(t *testing.T) {
	const (
		mimeTextPlain = "text/plain"
		mimeImagePNG  = "image/png"
		invalidByte   = 0xff
	)

	tests := []struct {
		name string
		file codeexecutor.File
		want bool
	}{
		{
			name: "empty content",
			file: codeexecutor.File{},
			want: true,
		},
		{
			name: "valid text",
			file: codeexecutor.File{
				Content:  contentHi,
				MIMEType: mimeTextPlain,
			},
			want: true,
		},
		{
			name: "non-text mime",
			file: codeexecutor.File{
				Content:  contentHi,
				MIMEType: mimeImagePNG,
			},
			want: false,
		},
		{
			name: "contains nul",
			file: codeexecutor.File{
				Content:  contentHi + "\x00",
				MIMEType: mimeTextPlain,
			},
			want: false,
		},
		{
			name: "invalid utf8",
			file: codeexecutor.File{
				Content:  string([]byte{invalidByte}),
				MIMEType: mimeTextPlain,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldInlineFileContent(tt.file))
		})
	}
}

func TestRunTool_AutoExportsOutToWorkspaceCache(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(ctx, enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.NotNil(t, out.PrimaryOutput)
	require.Equal(t, outATxt, out.PrimaryOutput.Name)
	require.Contains(t, out.PrimaryOutput.Content, contentHi)

	content, _, ok := toolcache.LookupSkillRunOutputFileFromContext(
		ctx,
		outATxt,
	)
	require.True(t, ok)
	require.Contains(t, content, contentHi)
}

func TestRunTool_DoesNotUseLoginShell(t *testing.T) {
	// A login shell would source ~/.bash_profile and set this variable.
	home := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(home, ".bash_profile"),
		[]byte("export TRPC_AGENT_TEST_LOGIN=1\n"),
		0o644,
	))
	t.Setenv("HOME", home)

	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: "echo $TRPC_AGENT_TEST_LOGIN",
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Equal(t, "\n", out.Stdout)
}

func TestRunTool_AutoPrependsVenvBinToPATH(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	cmd := strings.Join([]string{
		"set -e",
		"mkdir -p .venv/bin",
		"printf '%s\\n' '#!/usr/bin/env bash' 'echo OK' " +
			"> .venv/bin/hello",
		"chmod +x .venv/bin/hello",
		"hello",
	}, "; ")

	args := runInput{
		Skill:   testSkillName,
		Command: cmd,
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Contains(t, out.Stdout, "OK")
}

func TestVenvRelPaths_FromSkillRoot(t *testing.T) {
	cwd := path.Join(codeexecutor.DirSkills, testSkillName)
	venvRel, venvBinRel := venvRelPaths(cwd, testSkillName)
	require.Equal(t, skillDirVenv, venvRel)
	require.Equal(t, path.Join(skillDirVenv, "bin"), venvBinRel)
}

func TestVenvRelPaths_FromChildDir(t *testing.T) {
	cwd := path.Join(codeexecutor.DirSkills, testSkillName, scriptsDir)
	venvRel, venvBinRel := venvRelPaths(cwd, testSkillName)
	require.Equal(t, path.Join("..", skillDirVenv), venvRel)
	require.Equal(t, path.Join("..", skillDirVenv, "bin"), venvBinRel)
}

func TestInjectVenvEnv_PrependsPATHAndSetsVirtualEnv(t *testing.T) {
	env := map[string]string{
		envPath: "/usr/bin",
	}
	venv := path.Join(skillDirVenv)
	venvBin := path.Join(skillDirVenv, "bin")

	injectVenvEnv(env, venv, venvBin)

	require.Equal(t, venv, env[envVirtualEnv])
	sep := string(os.PathListSeparator)
	require.Equal(t, venvBin+sep+"/usr/bin", env[envPath])
}

func TestInjectVenvEnv_DoesNotOverrideVirtualEnv(t *testing.T) {
	const existing = "already"
	env := map[string]string{
		envVirtualEnv: existing,
		envPath:       "/bin",
	}
	venv := path.Join(skillDirVenv)
	venvBin := path.Join(skillDirVenv, "bin")

	injectVenvEnv(env, venv, venvBin)

	require.Equal(t, existing, env[envVirtualEnv])
	require.Contains(t, env[envPath], venvBin)
}

func TestInjectVenvEnv_EmptyPATHUsesVenvOnly(t *testing.T) {
	t.Setenv(envPath, "")
	env := map[string]string{}
	venv := path.Join(skillDirVenv)
	venvBin := path.Join(skillDirVenv, "bin")

	injectVenvEnv(env, venv, venvBin)

	require.Equal(t, venv, env[envVirtualEnv])
	require.Equal(t, venvBin, env[envPath])
}

func TestWrapWithVenvPrefix_BuildsExports(t *testing.T) {
	cmd := wrapWithVenvPrefix(cmdEcho, "VENV", "VENV/bin")
	require.Contains(t, cmd, "export "+envPath+"='VENV/bin'")
	require.Contains(t, cmd, "export "+envVirtualEnv+"='VENV'")
}

func TestRunTool_PrimaryOutput_SelectsByName(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt + "; echo " + contentHello +
			" > " + outBTxt,
		OutputFiles: []string{outATxt, outBTxt},
		Timeout:     timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.NotNil(t, out.PrimaryOutput)
	require.Equal(t, outATxt, out.PrimaryOutput.Name)
	require.Contains(t, out.PrimaryOutput.Content, contentHi)
}

func TestSelectPrimaryOutput_SkipsNonTextAndEmpty(t *testing.T) {
	large := strings.Repeat("a", maxPrimaryOutputChars+1)
	files := []runFile{
		{
			File: codeexecutor.File{
				Name:     "b.txt",
				Content:  "",
				MIMEType: "text/plain",
			},
		},
		{
			File: codeexecutor.File{
				Name:     "c.bin",
				Content:  "x",
				MIMEType: "application/octet-stream",
			},
		},
		{
			File: codeexecutor.File{
				Name:     "d.txt",
				Content:  large,
				MIMEType: "text/plain",
			},
		},
		{
			File: codeexecutor.File{
				Name:     "a.txt",
				Content:  "ok",
				MIMEType: "text/plain",
			},
		},
	}
	best := selectPrimaryOutput(files)
	require.NotNil(t, best)
	require.Equal(t, "a.txt", best.Name)
}

func TestRunTool_SaveAsArtifacts_AndOmitInline(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		OutputFiles:   []string{outGlobTxt},
		Timeout:       timeoutSecSmall,
		SaveArtifacts: true,
		OmitInline:    true,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	// Build invocation with artifact service and session info.
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			AppName: "app", UserID: "u", ID: "s1",
			State: session.StateMap{},
		}),
		agent.WithInvocationArtifactService(inmemory.NewService()),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	res, err := rt.Call(ctx, enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.ArtifactFiles, 1)
	require.Equal(t, outATxt, out.ArtifactFiles[0].Name)
	require.Equal(t, 0, out.ArtifactFiles[0].Version)
	// OmitInline should clear inline file contents.
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "", out.OutputFiles[0].Content)
}

func TestRunTool_SaveAsArtifacts_NoArtifactService(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		OutputFiles:   []string{outGlobTxt},
		Timeout:       timeoutSecSmall,
		SaveArtifacts: true,
		OmitInline:    true,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	// Invocation exists, but ArtifactService is nil.
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			AppName: "app", UserID: "u", ID: "s1",
			State: session.StateMap{},
		}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	res, err := rt.Call(ctx, enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Empty(t, out.ArtifactFiles)
	require.Len(t, out.Warnings, 1)
	require.Contains(t, out.Warnings[0], "artifact service")
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "", out.OutputFiles[0].Content)
}

func TestRunTool_SaveAsArtifacts_NoInvocation(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		OutputFiles:   []string{outGlobTxt},
		Timeout:       timeoutSecSmall,
		SaveArtifacts: true,
		OmitInline:    true,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Empty(t, out.ArtifactFiles)
	require.Len(t, out.Warnings, 2)
	require.Contains(t, out.Warnings[0], reasonNoInvocation)
	require.Len(t, out.OutputFiles, 1)
	require.Contains(t, out.OutputFiles[0].Content, contentHi)
}

func TestRunTool_SaveAsArtifacts_NoSession(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		OutputFiles:   []string{outGlobTxt},
		Timeout:       timeoutSecSmall,
		SaveArtifacts: true,
		OmitInline:    true,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationArtifactService(inmemory.NewService()),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	res, err := rt.Call(ctx, enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Empty(t, out.ArtifactFiles)
	require.Len(t, out.Warnings, 1)
	require.Contains(t, out.Warnings[0], reasonNoSession)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "", out.OutputFiles[0].Content)
}

func TestRunTool_SaveAsArtifacts_SessionMissingIDs(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		OutputFiles:   []string{outGlobTxt},
		Timeout:       timeoutSecSmall,
		SaveArtifacts: true,
		OmitInline:    true,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			AppName: "app", UserID: "u", ID: "",
			State: session.StateMap{},
		}),
		agent.WithInvocationArtifactService(inmemory.NewService()),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	res, err := rt.Call(ctx, enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Empty(t, out.ArtifactFiles)
	require.Len(t, out.Warnings, 1)
	require.Contains(t, out.Warnings[0], reasonNoSessionIDs)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "", out.OutputFiles[0].Content)
}

func TestRunTool_OmitInlineContent_AllowsReadByRef(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		OutputFiles: []string{outGlobTxt},
		Timeout:     timeoutSecSmall,
		OmitInline:  true,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	res, err := rt.Call(ctx, enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "", out.OutputFiles[0].Content)
	require.False(t, out.OutputFiles[0].Truncated)
	require.NotZero(t, out.OutputFiles[0].SizeBytes)

	got, _, handled, err := fileref.TryRead(ctx, out.OutputFiles[0].Ref)
	require.True(t, handled)
	require.NoError(t, err)
	require.Contains(t, got, contentHi)
}

func TestRunTool_OmitInlineContent_IncludesFileSize(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	const bytes5MiB = 5 * 1024 * 1024
	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; " +
			"head -c " + strconv.Itoa(bytes5MiB) +
			" /dev/zero > " + outATxt,
		OutputFiles: []string{outATxt},
		Timeout:     timeoutSecSmall,
		OmitInline:  true,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	res, err := rt.Call(ctx, enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "", out.OutputFiles[0].Content)
	require.Equal(t, int64(bytes5MiB), out.OutputFiles[0].SizeBytes)
	require.True(t, out.OutputFiles[0].Truncated)
}

func TestRunTool_OutputsSpec_AcceptsSnakeCaseJSON(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := map[string]any{
		"skill": testSkillName,
		"command": "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		"outputs": map[string]any{
			"globs":         []string{"$OUTPUT_DIR/*.txt"},
			"inline":        true,
			"save":          false,
			"max_files":     10,
			"name_template": "pref/",
		},
		"timeout": timeoutSecSmall,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Len(t, out.OutputFiles, 1)
	require.Contains(t, out.OutputFiles[0].Content, contentHi)
}

// errArtifactService always fails on save to cover error path.
type errArtifactService struct{}

func (e *errArtifactService) SaveArtifact(
	ctx context.Context, sessionInfo artifact.SessionInfo,
	filename string, a *artifact.Artifact,
) (int, error) {
	return 0, fmt.Errorf("forced-error")
}
func (e *errArtifactService) LoadArtifact(
	ctx context.Context, sessionInfo artifact.SessionInfo,
	filename string, version *int,
) (*artifact.Artifact, error) {
	return nil, nil
}
func (e *errArtifactService) ListArtifactKeys(
	ctx context.Context, sessionInfo artifact.SessionInfo,
) ([]string, error) {
	return nil, nil
}
func (e *errArtifactService) DeleteArtifact(
	ctx context.Context, sessionInfo artifact.SessionInfo,
	filename string,
) error {
	return nil
}
func (e *errArtifactService) ListVersions(
	ctx context.Context, sessionInfo artifact.SessionInfo,
	filename string,
) ([]int, error) {
	return nil, nil
}

func TestRunTool_SaveAsArtifacts_SaveError(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		OutputFiles:   []string{outGlobTxt},
		Timeout:       timeoutSecSmall,
		SaveArtifacts: true,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			AppName: "app", UserID: "u", ID: "s1",
			State: session.StateMap{},
		}),
		agent.WithInvocationArtifactService(&errArtifactService{}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	_, err = rt.Call(ctx, enc)
	require.Error(t, err)
}

func TestRunTool_ErrorOnMissingSkill(t *testing.T) {
	root := t.TempDir()
	// No skill written.
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{Skill: "missing", Command: "echo " + contentHello}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)
	_, err = rt.Call(context.Background(), enc)
	require.Error(t, err)
}

func TestRunTool_AllowedCommands_AllowsSingleCommand(t *testing.T) {
	t.Setenv(envAllowedCommands, cmdEcho)

	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: echoOK,
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Contains(t, out.Stdout, "ok")
}

func TestRunTool_AllowedCommands_AllowsBasenameMatch(t *testing.T) {
	t.Setenv(envAllowedCommands, cmdEcho+","+cmdLS)

	full, err := exec.LookPath(cmdLS)
	require.NoError(t, err)

	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: full,
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
}

func TestRunTool_AllowedCommands_RejectsDisallowedCommand(t *testing.T) {
	t.Setenv(envAllowedCommands, cmdEcho)

	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: cmdLS,
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	_, err = rt.Call(context.Background(), enc)
	require.Error(t, err)
}

func TestRunTool_AllowedCommands_RejectsShellSyntax(t *testing.T) {
	t.Setenv(envAllowedCommands, cmdEcho)

	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: cmdEchoThenLS,
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	_, err = rt.Call(context.Background(), enc)
	require.Error(t, err)
}

func TestRunTool_DeniedCommands_RejectsDeniedCommand(t *testing.T) {
	t.Setenv(envDeniedCommands, cmdEcho)

	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: echoOK,
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	_, err = rt.Call(context.Background(), enc)
	require.Error(t, err)
}

func TestRunTool_DeniedCommands_AllowsOtherCommand(t *testing.T) {
	t.Setenv(envDeniedCommands, cmdEcho)

	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: cmdLS,
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
}

func TestRunTool_AllowedAndDenied_DeniedWins(t *testing.T) {
	t.Setenv(envAllowedCommands, cmdEcho+","+cmdLS)
	t.Setenv(envDeniedCommands, cmdEcho)

	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: echoOK,
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	_, err = rt.Call(context.Background(), enc)
	require.Error(t, err)
}

func TestRunTool_AllowedCommands_OptionOverridesEnv(t *testing.T) {
	t.Setenv(envAllowedCommands, cmdLS)

	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec, WithAllowedCommands(cmdEcho))

	allow := runInput{
		Skill:   testSkillName,
		Command: echoOK,
		Timeout: timeoutSecSmall,
	}
	allowEnc, err := jsonMarshal(allow)
	require.NoError(t, err)
	_, err = rt.Call(context.Background(), allowEnc)
	require.NoError(t, err)

	block := runInput{
		Skill:   testSkillName,
		Command: cmdLS,
		Timeout: timeoutSecSmall,
	}
	blockEnc, err := jsonMarshal(block)
	require.NoError(t, err)
	_, err = rt.Call(context.Background(), blockEnc)
	require.Error(t, err)
}

func TestRunTool_DeniedCommands_OptionOverridesEnv(t *testing.T) {
	t.Setenv(envDeniedCommands, cmdLS)

	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec, WithDeniedCommands(cmdEcho))

	deny := runInput{
		Skill:   testSkillName,
		Command: echoOK,
		Timeout: timeoutSecSmall,
	}
	denyEnc, err := jsonMarshal(deny)
	require.NoError(t, err)
	_, err = rt.Call(context.Background(), denyEnc)
	require.Error(t, err)

	allow := runInput{
		Skill:   testSkillName,
		Command: cmdLS,
		Timeout: timeoutSecSmall,
	}
	allowEnc, err := jsonMarshal(allow)
	require.NoError(t, err)
	_, err = rt.Call(context.Background(), allowEnc)
	require.NoError(t, err)
}

func TestRunTool_setAllowedCommands_TrimsAndSkipsEmpty(t *testing.T) {
	rt := &RunTool{}
	rt.setAllowedCommands(nil)
	require.Nil(t, rt.allowedCmds)

	rt.setAllowedCommands([]string{"", "  ", cmdEcho})
	require.Contains(t, rt.allowedCmds, cmdEcho)

	rt.setAllowedCommands([]string{cmdLS})
	require.Contains(t, rt.allowedCmds, cmdLS)
}

func TestRunTool_setDeniedCommands_TrimsAndSkipsEmpty(t *testing.T) {
	rt := &RunTool{}
	rt.setDeniedCommands(nil)
	require.Nil(t, rt.deniedCmds)

	rt.setDeniedCommands([]string{"", "  ", cmdEcho})
	require.Contains(t, rt.deniedCmds, cmdEcho)

	rt.setDeniedCommands([]string{cmdLS})
	require.Contains(t, rt.deniedCmds, cmdLS)
}

func TestSplitCommandLine(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{
			name: "simple",
			in:   "echo ok",
			want: []string{"echo", "ok"},
		},
		{
			name: "double_quote",
			in:   `echo "hi there"`,
			want: []string{"echo", "hi there"},
		},
		{
			name: "single_quote",
			in:   "echo 'hi there'",
			want: []string{"echo", "hi there"},
		},
		{
			name: "escaped_space",
			in:   "echo hi\\ there",
			want: []string{"echo", "hi there"},
		},
		{
			name:    "shell_meta",
			in:      cmdEchoThenLS,
			wantErr: true,
		},
		{
			name:    "unterminated_quote",
			in:      `echo "hi`,
			wantErr: true,
		},
		{
			name:    "trailing_escape",
			in:      "echo hi\\",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitCommandLine(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRunTool_ErrorOnNilExecutor(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	rt := NewRunTool(repo, nil)
	args := runInput{Skill: testSkillName, Command: echoOK}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)
	_, err = rt.Call(context.Background(), enc)
	require.Error(t, err)
}

// jsonMarshal is a tiny wrapper to keep tests tidy.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// stubCE implements minimal CodeExecutor to trigger engine fallback.
type stubCE struct{}

func (s *stubCE) ExecuteCode(
	ctx context.Context, in codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{Output: ""}, nil
}
func (s *stubCE) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func TestRunTool_FallbackEngine_NoEngineProvider(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	// Use a CodeExecutor without Engine() to trigger fallback.
	exec := &stubCE{}
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:   testSkillName,
		Command: echoOK,
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
}

// Test that when no cwd is provided, the working directory defaults to
// the staged skill root so relative paths in skill docs work.
func TestRunTool_DefaultCWD_UsesSkillRoot(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)
	// Place a file under scripts/ inside the skill.
	scripts := filepath.Join(dir, scriptsDir)
	require.NoError(t, os.MkdirAll(scripts, 0o755))
	data := []byte(contentHello + "\n")
	require.NoError(t, os.WriteFile(
		filepath.Join(scripts, "file.txt"), data, 0o644,
	))

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:       testSkillName,
		Command:     "cat " + scriptsDir + "/file.txt > " + outATxt,
		OutputFiles: []string{outATxt},
		Timeout:     timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, outATxt, out.OutputFiles[0].Name)
	require.Contains(t, out.OutputFiles[0].Content, contentHello)
}

// Test that a relative cwd is resolved under the skill root, not under
// the workspace root.
func TestRunTool_RelativeCWD_SubpathUnderSkillRoot(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)
	scripts := filepath.Join(dir, scriptsDir)
	require.NoError(t, os.MkdirAll(scripts, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(scripts, "msg.txt"),
		[]byte(contentMsg+"\n"), 0o644,
	))

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill:       testSkillName,
		Cwd:         scriptsDir,
		Command:     "cat msg.txt > ../" + outBTxt,
		OutputFiles: []string{outBTxt},
		Timeout:     timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, outBTxt, out.OutputFiles[0].Name)
	require.Contains(t, out.OutputFiles[0].Content, contentMsg)
}

func TestRunTool_RelativeCWD_TraversalDoesNotEscapeWorkspace(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	args := runInput{
		Skill: testSkillName,
		Cwd:   "../../..",
		Command: "pwd; echo \"$" +
			codeexecutor.WorkspaceEnvDirKey + "\"",
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)

	out := res.(runOutput)
	lines := strings.Split(strings.TrimSpace(out.Stdout), "\n")
	require.GreaterOrEqual(t, len(lines), 2)

	pwd := strings.TrimSpace(lines[0])
	wsRoot := strings.TrimSpace(lines[1])

	pwdResolved, err := filepath.EvalSymlinks(pwd)
	require.NoError(t, err)
	wsRootResolved, err := filepath.EvalSymlinks(wsRoot)
	require.NoError(t, err)

	rel, err := filepath.Rel(wsRootResolved, pwdResolved)
	require.NoError(t, err)
	require.False(t, strings.HasPrefix(rel, ".."))
}

func TestResolveCWD_WorkspaceEnvPathAllowlist(t *testing.T) {
	base := path.Join(codeexecutor.DirSkills, "x")

	// traversal should fallback
	require.Equal(t, base, resolveCWD("$"+codeexecutor.WorkspaceEnvDirKey+"/../..", "x"))
	require.Equal(t, base, resolveCWD("$"+codeexecutor.WorkspaceEnvDirKey+"\\..\\..", "x"))

	// allowed roots under workspace
	require.Equal(t, codeexecutor.DirWork, resolveCWD("$"+codeexecutor.WorkspaceEnvDirKey+"/"+codeexecutor.DirWork, "x"))
	require.Equal(t, codeexecutor.DirSkills+"/x", resolveCWD("$"+codeexecutor.WorkspaceEnvDirKey+"/"+codeexecutor.DirSkills+"/x", "x"))

	// disallowed root under workspace falls back to base
	require.Equal(t, base, resolveCWD("$"+codeexecutor.WorkspaceEnvDirKey+"/etc", "x"))
}

func TestResolveCWD_AbsPathAllowlist(t *testing.T) {
	base := path.Join(codeexecutor.DirSkills, "x")

	require.Equal(t, codeexecutor.DirWork, resolveCWD("/"+codeexecutor.DirWork, "x"))
	require.Equal(t, base, resolveCWD("/etc", "x"))
	require.Equal(t, ".", resolveCWD("/", "x"))
}

func TestResolveCWD_RelPath_BackslashTraversalDoesNotEscape(t *testing.T) {
	base := path.Join(codeexecutor.DirSkills, "x")
	require.Equal(t, base, resolveCWD("..\\..\\..", "x"))
}

// Validate Declaration basics and required fields.
func TestRunTool_Declaration(t *testing.T) {
	t.Setenv(envAllowedCommands, "")
	t.Setenv(envDeniedCommands, "")
	rt := NewRunTool(nil, nil)
	d := rt.Declaration()
	require.NotNil(t, d)
	require.Equal(t, "skill_run", d.Name)
	require.NotNil(t, d.InputSchema)
	require.Contains(t, d.InputSchema.Required, "skill")
	require.Contains(t, d.InputSchema.Required, "command")
	require.Equal(t, "Shell command", d.InputSchema.Properties["command"].Description)
}

func TestRunTool_Declaration_IncludesAllowedCommandsPreview(t *testing.T) {
	cmds := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		cmds = append(cmds, fmt.Sprintf("cmd%02d", i))
	}
	rt := NewRunTool(nil, nil, WithAllowedCommands(cmds...))
	d := rt.Declaration()
	require.NotNil(t, d)
	require.Contains(t, d.Description, "Restrictions enabled")
	require.Contains(t, d.Description, "Allowed commands:")
	require.Contains(t, d.Description, "cmd00")
	require.Contains(t, d.Description, "cmd19")
	require.Contains(t, d.Description, "(+5 more)")
	require.NotContains(t, d.Description, "cmd24")
	require.Contains(t, d.InputSchema.Properties["command"].Description, "no shell syntax")
}

// Ensure parseRunArgs rejects invalid JSON and missing fields.
func TestRunTool_parseRunArgs_Validation(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	// Invalid JSON
	_, err = rt.parseRunArgs([]byte("not-json"))
	require.Error(t, err)

	// Missing command
	b, _ := json.Marshal(map[string]any{"skill": testSkillName})
	_, err = rt.parseRunArgs(b)
	require.Error(t, err)
}

// collectFiles should return nil slice and nil error on empty patterns.
func TestRunTool_collectFiles_EmptyPatterns(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())
	eng := rt.ensureEngine()
	ws, err := rt.createWorkspace(context.Background(), eng, "sess")
	require.NoError(t, err)
	files, err := rt.collectFiles(
		context.Background(), eng, ws, nil,
	)
	require.NoError(t, err)
	require.Nil(t, files)
}

// shellQuote should escape single quotes safely.
func TestShellQuote(t *testing.T) {
	got := shellQuote("a'b")
	// Expect: 'a'\''b'
	require.Equal(t, "'a'\\''b'", got)
	require.Equal(t, "''", shellQuote(""))
}

func TestAppendWarning_SkipsNilOrEmpty(t *testing.T) {
	appendWarning(nil, reasonNoInvocation)
	out := &runOutput{}
	appendWarning(out, "")
	require.Empty(t, out.Warnings)

	appendWarning(out, reasonNoInvocation)
	require.Len(t, out.Warnings, 1)
}

// filepathBase trims trailing slash and returns last element.
func TestFilepathBase(t *testing.T) {
	require.Equal(t, "b", filepathBase("/a/b/"))
	require.Equal(t, "a", filepathBase("a"))
}

// Using Outputs spec with Inline=true maps manifest into OutputFiles
// and does not attach artifact refs when Save=false.
func TestRunTool_OutputsSpec_Inline_NoSave(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		Outputs: &codeexecutor.OutputSpec{
			Globs:  []string{outGlobTxt},
			Inline: true,
			Save:   false,
		},
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, outATxt, out.OutputFiles[0].Name)
	require.Contains(t, out.OutputFiles[0].Content, contentHi)
	require.Len(t, out.ArtifactFiles, 0)
}

// Using Outputs spec with Save=true and Inline=false should attach
// artifact refs from manifest without inlining file content.
func TestRunTool_OutputsSpec_Save_NoInline(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		Outputs: &codeexecutor.OutputSpec{
			Globs:        []string{outGlobTxt},
			Inline:       false,
			Save:         true,
			NameTemplate: "pref/",
		},
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	// Provide artifact service and session for saving.
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			AppName: "app", UserID: "u", ID: "s1",
			State: session.StateMap{},
		}),
		agent.WithInvocationArtifactService(inmemory.NewService()),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	res, err := rt.Call(ctx, enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.OutputFiles, 0)
	require.Len(t, out.ArtifactFiles, 1)
	require.Equal(t, "pref/"+outATxt, out.ArtifactFiles[0].Name)
}

// Verify SaveArtifacts prefixing and OmitInline together on legacy path.
func TestRunTool_SaveArtifacts_PrefixAndOmitInline(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := runInput{
		Skill: testSkillName,
		Command: "mkdir -p out; echo " + contentHi +
			" > " + outATxt,
		OutputFiles:    []string{outGlobTxt},
		Timeout:        timeoutSecSmall,
		SaveArtifacts:  true,
		OmitInline:     true,
		ArtifactPrefix: "pfx/",
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			AppName: "app", UserID: "u", ID: "s1",
			State: session.StateMap{},
		}),
		agent.WithInvocationArtifactService(inmemory.NewService()),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	res, err := rt.Call(ctx, enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.ArtifactFiles, 1)
	require.Equal(t, "pfx/"+outATxt, out.ArtifactFiles[0].Name)
	require.Len(t, out.OutputFiles, 1)
	require.Equal(t, "", out.OutputFiles[0].Content)
}

// Cover createWorkspace branch where internal registry is nil.
func TestRunTool_CreateWorkspace_NilRegistry(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())
	// Force nil to exercise initialization path.
	rt.reg = nil

	args := runInput{Skill: testSkillName, Command: echoOK,
		Timeout: timeoutSecSmall}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)
	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
}

// StageInputs using a skill:// reference and consume it.
func TestRunTool_StageInputs_FromSkill(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)
	// Prepare a source file under scripts/ of the skill.
	scripts := filepath.Join(dir, scriptsDir)
	require.NoError(t, os.MkdirAll(scripts, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(scripts, "msg.txt"),
		[]byte(contentMsg+"\n"), 0o644,
	))

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := runInput{
		Skill:   testSkillName,
		Command: "cat work/inputs/m.txt > " + outBTxt,
		Inputs: []codeexecutor.InputSpec{
			{From: "skill://" + testSkillName + "/" + scriptsDir +
				"/msg.txt",
				To:   "work/inputs/m.txt",
				Mode: "copy",
			},
		},
		OutputFiles: []string{outBTxt},
		Timeout:     timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)

	res, err := rt.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(runOutput)
	require.Equal(t, 0, out.ExitCode)
	require.Len(t, out.OutputFiles, 1)
	require.Contains(t, out.OutputFiles[0].Content, contentMsg)
}

func TestRunTool_Call_InvalidInputSpec_Error(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	args := runInput{
		Skill:   testSkillName,
		Command: echoOK,
		Inputs: []codeexecutor.InputSpec{{
			From: "unknown://abc",
			To:   "work/inputs/x",
			Mode: "copy",
		}},
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)
	_, err = rt.Call(context.Background(), enc)
	require.Error(t, err)
}

func TestRunTool_stageSkill_WorkspaceRootFileError(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())
	eng := localexec.New().Engine()

	// Workspace path points to a file; staging should fail.
	tmpf := filepath.Join(t.TempDir(), "asfile")
	require.NoError(t, os.WriteFile(tmpf, []byte("x"), 0o644))
	ws := codeexecutor.Workspace{ID: "bad", Path: tmpf}

	err = rt.stageSkill(context.Background(), eng, ws, dir,
		testSkillName,
	)
	require.Error(t, err)
}

func TestRunTool_stageSkill_DirDigestError(t *testing.T) {
	rt := &RunTool{}
	eng := &fakeEngine{}
	ws := codeexecutor.Workspace{ID: "x", Path: t.TempDir()}

	missing := filepath.Join(t.TempDir(), "missing-skill")
	err := rt.stageSkill(context.Background(), eng, ws, missing,
		testSkillName,
	)
	require.Error(t, err)
}

type errFS struct{}

func (e *errFS) PutFiles(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ []codeexecutor.PutFile,
) error {
	return nil
}

func (e *errFS) StageDirectory(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ string,
	_ string,
	_ codeexecutor.StageOptions,
) error {
	return fmt.Errorf("forced-error")
}

func (e *errFS) Collect(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ []string,
) ([]codeexecutor.File, error) {
	return nil, nil
}

func (e *errFS) StageInputs(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ []codeexecutor.InputSpec,
) error {
	return nil
}

func (e *errFS) CollectOutputs(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

type fsFailEngine struct {
	f codeexecutor.WorkspaceFS
}

func (e *fsFailEngine) Manager() codeexecutor.WorkspaceManager {
	return nil
}

func (e *fsFailEngine) FS() codeexecutor.WorkspaceFS { return e.f }

func (e *fsFailEngine) Runner() codeexecutor.ProgramRunner { return nil }

func (e *fsFailEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{}
}

func TestRunTool_stageSkill_StageDirectoryError(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)

	rt := &RunTool{}
	eng := &fsFailEngine{f: &errFS{}}
	ws := codeexecutor.Workspace{ID: "x", Path: t.TempDir()}

	err := rt.stageSkill(context.Background(), eng, ws, dir,
		testSkillName,
	)
	require.Error(t, err)
}

func TestRunTool_stageSkill_LoadMetadataError(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)

	ws := codeexecutor.Workspace{ID: "x", Path: t.TempDir()}
	_, err := codeexecutor.EnsureLayout(ws.Path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(ws.Path, codeexecutor.MetaFileName),
		[]byte("{"),
		0o644,
	))

	rt := &RunTool{}
	eng := localexec.New().Engine()
	err = rt.stageSkill(context.Background(), eng, ws, dir,
		testSkillName,
	)
	require.Error(t, err)
}

func TestRunTool_stageSkill_CreatesInputsDir(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	rt := NewRunTool(repo, localexec.New())

	workRoot, err := os.MkdirTemp("", "skill-stage-")
	require.NoError(t, err)
	t.Cleanup(func() {
		makeTreeWritable(workRoot)
		_ = os.RemoveAll(workRoot)
	})

	loc := localexec.NewRuntime(workRoot)
	fs := &countingFS{inner: loc}
	eng := &countingEngine{m: loc, f: fs, r: loc}

	ctx := context.Background()
	ws, err := eng.Manager().CreateWorkspace(
		ctx, "stage-inputs-dir", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)

	err = rt.stageSkill(ctx, eng, ws, dir, testSkillName)
	require.NoError(t, err)
	require.Equal(t, 1, fs.stageCalls)

	err = rt.stageSkill(ctx, eng, ws, dir, testSkillName)
	require.NoError(t, err)
	require.Equal(t, 1, fs.stageCalls)

	inputsPath := filepath.Join(
		ws.Path, codeexecutor.DirWork, "inputs",
	)
	info, err := os.Stat(inputsPath)
	require.NoError(t, err)
	require.True(t, info.IsDir())

	venvPath := filepath.Join(
		ws.Path,
		codeexecutor.DirSkills,
		testSkillName,
		skillDirVenv,
	)
	info, err = os.Stat(venvPath)
	require.NoError(t, err)
	require.True(t, info.IsDir())

	err = os.WriteFile(
		filepath.Join(venvPath, "writable.txt"),
		[]byte("ok"),
		0o644,
	)
	require.NoError(t, err)
}

func makeTreeWritable(root string) {
	if root == "" {
		return
	}
	_ = filepath.Walk(root, func(p string, info os.FileInfo,
		err error,
	) error {
		if err != nil || info == nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		_ = os.Chmod(p, info.Mode()|0o200)
		return nil
	})
}

func TestResolveCWD_AbsolutePath(t *testing.T) {
	base := path.Join(codeexecutor.DirSkills, testSkillName)

	// "/" means workspace root.
	got := resolveCWD("/", testSkillName)
	require.Equal(t, ".", got)

	// Workspace-absolute paths are normalized to workspace-relative.
	got = resolveCWD("/skills/other", testSkillName)
	require.Equal(t, "skills/other", got)
	got = resolveCWD("/work", testSkillName)
	require.Equal(t, "work", got)
	got = resolveCWD("/out/x", testSkillName)
	require.Equal(t, "out/x", got)
	got = resolveCWD("/runs/r1", testSkillName)
	require.Equal(t, "runs/r1", got)

	// Host-absolute paths are rejected and fall back to skill root.
	got = resolveCWD("/Users/example", testSkillName)
	require.Equal(t, base, got)
}

func TestResolveCWD_DefaultAndRelative(t *testing.T) {
	// Default: when cwd is empty, base is skills/<name>.
	base := path.Join(codeexecutor.DirSkills, testSkillName)
	got := resolveCWD("", testSkillName)
	require.Equal(t, base, got)

	// Relative: appended under the skill root.
	got = resolveCWD("sub/dir", testSkillName)
	require.Equal(t, path.Join(base, "sub/dir"), got)
}

func TestResolveCWD_WorkspaceEnvPrefixes(t *testing.T) {
	got := resolveCWD("$WORK_DIR", testSkillName)
	require.Equal(t, codeexecutor.DirWork, got)

	got = resolveCWD("${OUTPUT_DIR}/x", testSkillName)
	require.Equal(t, path.Join(codeexecutor.DirOut, "x"), got)
}

func TestBuildRunOutput_TruncatesStdoutStderr(t *testing.T) {
	long := strings.Repeat("a", maxOutputChars+1)
	rr := codeexecutor.RunResult{
		Stdout:   long,
		Stderr:   long,
		ExitCode: 0,
	}
	out := buildRunOutput(rr, nil)
	require.Len(t, out.Warnings, 2)
	require.Equal(t, maxOutputChars, len(out.Stdout))
	require.Equal(t, maxOutputChars, len(out.Stderr))
}

type countingFS struct {
	inner      codeexecutor.WorkspaceFS
	stageCalls int
}

func (c *countingFS) PutFiles(
	ctx context.Context,
	ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	return c.inner.PutFiles(ctx, ws, files)
}

func (c *countingFS) StageDirectory(
	ctx context.Context,
	ws codeexecutor.Workspace,
	src string,
	to string,
	opt codeexecutor.StageOptions,
) error {
	c.stageCalls++
	return c.inner.StageDirectory(ctx, ws, src, to, opt)
}

func (c *countingFS) Collect(
	ctx context.Context,
	ws codeexecutor.Workspace,
	patterns []string,
) ([]codeexecutor.File, error) {
	return c.inner.Collect(ctx, ws, patterns)
}

func (c *countingFS) StageInputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	return c.inner.StageInputs(ctx, ws, specs)
}

func (c *countingFS) CollectOutputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return c.inner.CollectOutputs(ctx, ws, spec)
}

type countingEngine struct {
	m codeexecutor.WorkspaceManager
	f *countingFS
	r codeexecutor.ProgramRunner
}

func (e *countingEngine) Manager() codeexecutor.WorkspaceManager {
	return e.m
}

func (e *countingEngine) FS() codeexecutor.WorkspaceFS { return e.f }

func (e *countingEngine) Runner() codeexecutor.ProgramRunner {
	return e.r
}

func (e *countingEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{}
}

type recordingRunner struct {
	last codeexecutor.RunProgramSpec
}

func (r *recordingRunner) RunProgram(
	_ context.Context,
	_ codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	r.last = spec
	return codeexecutor.RunResult{}, nil
}

type fakeEngine struct {
	r codeexecutor.ProgramRunner
}

func (e *fakeEngine) Manager() codeexecutor.WorkspaceManager { return nil }
func (e *fakeEngine) FS() codeexecutor.WorkspaceFS           { return nil }
func (e *fakeEngine) Runner() codeexecutor.ProgramRunner     { return e.r }
func (e *fakeEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{}
}

func TestRunTool_runProgram_DefaultTimeout(t *testing.T) {
	rr := &recordingRunner{}
	eng := &fakeEngine{r: rr}
	ws := codeexecutor.Workspace{ID: "x", Path: t.TempDir()}
	rt := &RunTool{}

	_, err := rt.runProgram(
		context.Background(),
		eng,
		ws,
		".",
		runInput{Skill: testSkillName, Command: echoOK},
	)
	require.NoError(t, err)
	require.Equal(t, defaultSkillRunTimeout, rr.last.Timeout)

	rr.last = codeexecutor.RunProgramSpec{}
	_, err = rt.runProgram(
		context.Background(),
		eng,
		ws,
		".",
		runInput{Skill: testSkillName, Command: echoOK, Timeout: 1},
	)
	require.NoError(t, err)
	require.Equal(t, 1*time.Second, rr.last.Timeout)
}

// dummyExec implements CodeExecutor but not EngineProvider to cover
// ensureEngine fallback path.
type dummyExec struct{}

func (*dummyExec) ExecuteCode(
	_ context.Context, _ codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (*dummyExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func TestRunTool_EnsureEngine_Fallback(t *testing.T) {
	// Repo is not used by ensureEngine.
	rt := NewRunTool(&mockRepo{}, &dummyExec{})
	eng := rt.ensureEngine()
	require.NotNil(t, eng)
	// Create a workspace to ensure the engine is usable.
	ws, err := eng.Manager().CreateWorkspace(
		context.Background(), "eng-fallback", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	require.NotEmpty(t, ws.Path)
}

func TestFilepathBase_Variants(t *testing.T) {
	require.Equal(t, "c", filepathBase("a/b/c"))
	require.Equal(t, "b", filepathBase("a/b/"))
	require.Equal(t, "root", filepathBase("root"))
}

func TestParseRunArgs_InvalidJSON(t *testing.T) {
	rt := NewRunTool(&mockRepo{}, nil)
	_, err := rt.parseRunArgs([]byte("{bad}"))
	require.Error(t, err)
}

func TestMergeManifestArtifactRefs_Appends(t *testing.T) {
	mf := &codeexecutor.OutputManifest{
		Files: []codeexecutor.FileRef{{
			Name:     "out/a.txt",
			SavedAs:  "prefix-out/a.txt",
			Version:  2,
			MIMEType: "text/plain",
		}},
	}
	out := &runOutput{}
	mergeManifestArtifactRefs(mf, out)
	require.Len(t, out.ArtifactFiles, 1)
	require.Equal(t, "prefix-out/a.txt", out.ArtifactFiles[0].Name)
	require.Equal(t, 2, out.ArtifactFiles[0].Version)
}

// Test that workspace persists across calls within the same session,
// so files written earlier can be collected later.
func TestRunTool_WorkspacePersistsAcrossCalls(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)

	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	exec := localexec.New()
	rt := NewRunTool(repo, exec)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			AppName: "app", UserID: "u", ID: "sess-1",
			State: session.StateMap{},
		}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	// First call: create a file under out/.
	a1 := runInput{
		Skill:   testSkillName,
		Command: "mkdir -p out; echo hi > out/p.txt",
		OutputFiles: []string{
			"out/p.txt",
		},
		Timeout: timeoutSecSmall,
	}
	b1, err := jsonMarshal(a1)
	require.NoError(t, err)
	r1, err := rt.Call(ctx, b1)
	require.NoError(t, err)
	o1 := r1.(runOutput)
	require.Equal(t, 0, o1.ExitCode)
	require.Len(t, o1.OutputFiles, 1)
	require.Contains(t, o1.OutputFiles[0].Content, "hi")

	// Second call: do not write; just collect the same file.
	a2 := runInput{
		Skill:   testSkillName,
		Command: "echo ok",
		OutputFiles: []string{
			"out/p.txt",
		},
		Timeout: timeoutSecSmall,
	}
	b2, err := jsonMarshal(a2)
	require.NoError(t, err)
	r2, err := rt.Call(ctx, b2)
	require.NoError(t, err)
	o2 := r2.(runOutput)
	require.Equal(t, 0, o2.ExitCode)
	require.Len(t, o2.OutputFiles, 1)
	require.Contains(t, o2.OutputFiles[0].Content, "hi")
}

func TestSkillStagingHelpers_EarlyReturns(t *testing.T) {
	rt := &RunTool{}
	ctx := context.Background()
	ws := codeexecutor.Workspace{}

	ok, err := rt.skillLinksPresent(ctx, nil, ws, "")
	require.NoError(t, err)
	require.False(t, ok)

	ok, err = rt.skillLinksPresent(ctx, nil, ws, testSkillName)
	require.Error(t, err)
	require.False(t, ok)

	require.NoError(t, rt.removeWorkspacePath(ctx, nil, ws, ""))
	require.Error(t, rt.removeWorkspacePath(
		ctx,
		nil,
		ws,
		path.Join(codeexecutor.DirSkills, testSkillName),
	))
}

type stubFS struct {
	collectFiles []codeexecutor.File
	collectErr   error

	putErr   error
	putCalls int
	putFiles []codeexecutor.PutFile
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

type stubEngine struct {
	f codeexecutor.WorkspaceFS
	r codeexecutor.ProgramRunner
}

func (*stubEngine) Manager() codeexecutor.WorkspaceManager { return nil }
func (e *stubEngine) FS() codeexecutor.WorkspaceFS         { return e.f }
func (e *stubEngine) Runner() codeexecutor.ProgramRunner   { return e.r }

func (*stubEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{}
}

func TestRunTool_loadWorkspaceMetadata_CoversBranches(t *testing.T) {
	rt := &RunTool{}
	ctx := context.Background()
	ws := codeexecutor.Workspace{}

	_, err := rt.loadWorkspaceMetadata(ctx, nil, ws)
	require.Error(t, err)

	fs := &stubFS{collectErr: fmt.Errorf(errCollectFail)}
	eng := &stubEngine{f: fs}
	_, err = rt.loadWorkspaceMetadata(ctx, eng, ws)
	require.Error(t, err)

	fs.collectErr = nil
	md, err := rt.loadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotNil(t, md.Skills)

	fs.collectFiles = []codeexecutor.File{{
		Name:    codeexecutor.MetaFileName,
		Content: metadataWhitespace,
	}}
	md, err = rt.loadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotNil(t, md.Skills)

	fs.collectFiles = []codeexecutor.File{{
		Name:    codeexecutor.MetaFileName,
		Content: metadataZeroValuesJSON,
	}}
	start := time.Now()
	md, err = rt.loadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotNil(t, md.Skills)
	require.False(t, md.CreatedAt.IsZero())
	require.False(t, md.CreatedAt.Before(start))
}

func TestRunTool_saveWorkspaceMetadata_CoversBranches(t *testing.T) {
	rt := &RunTool{}
	ctx := context.Background()
	ws := codeexecutor.Workspace{ID: "x", Path: t.TempDir()}
	md := codeexecutor.WorkspaceMetadata{}

	err := rt.saveWorkspaceMetadata(ctx, nil, ws, md)
	require.Error(t, err)

	fs := &stubFS{}
	eng := &stubEngine{f: fs}
	err = rt.saveWorkspaceMetadata(ctx, eng, ws, md)
	require.Error(t, err)

	r := &stubRunner{}
	eng.r = r
	fs.putErr = fmt.Errorf(errPutFail)
	err = rt.saveWorkspaceMetadata(ctx, eng, ws, md)
	require.Error(t, err)
	require.Equal(t, 1, fs.putCalls)
	require.Equal(t, 0, r.calls)
	require.Equal(t, workspaceMetadataTmpFile, fs.putFiles[0].Path)
	require.Equal(t, workspaceMetadataFileMode, fs.putFiles[0].Mode)

	fs.putErr = nil
	r.err = fmt.Errorf(errRunFail)
	err = rt.saveWorkspaceMetadata(ctx, eng, ws, md)
	require.Error(t, err)
	require.Equal(t, 2, fs.putCalls)
	require.Equal(t, 1, r.calls)
	require.Equal(t, "bash", r.lastSpec.Cmd)
	require.Len(t, r.lastSpec.Args, 2)
	require.Contains(t, r.lastSpec.Args[1], "mv -f")
}

func TestRunTool_skillLinksPresent_ExitCodes(t *testing.T) {
	rt := &RunTool{}
	ctx := context.Background()
	ws := codeexecutor.Workspace{}

	r := &stubRunner{res: codeexecutor.RunResult{ExitCode: 1}}
	eng := &stubEngine{r: r}

	ok, err := rt.skillLinksPresent(ctx, eng, ws, testSkillName)
	require.NoError(t, err)
	require.False(t, ok)

	r.err = fmt.Errorf(errRunFail)
	ok, err = rt.skillLinksPresent(ctx, eng, ws, testSkillName)
	require.Error(t, err)
	require.False(t, ok)

	r.res = codeexecutor.RunResult{ExitCode: 0}
	r.err = fmt.Errorf(errRunFail)
	ok, err = rt.skillLinksPresent(ctx, eng, ws, testSkillName)
	require.Error(t, err)
	require.False(t, ok)
}
