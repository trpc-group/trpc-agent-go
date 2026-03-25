//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestParseGitHubLocation(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want gitHubLocation
	}{
		{
			name: "tree",
			raw: "https://github.com/owner/repo/tree/main/" +
				"skills/hello",
			want: gitHubLocation{
				Owner:   "owner",
				Repo:    "repo",
				Ref:     "main",
				DirPath: "skills/hello",
			},
		},
		{
			name: "blob_skill_md",
			raw: "https://github.com/owner/repo/blob/main/" +
				"skills/hello/SKILL.md",
			want: gitHubLocation{
				Owner:   "owner",
				Repo:    "repo",
				Ref:     "main",
				DirPath: "skills/hello",
			},
		},
		{
			name: "raw_skill_md",
			raw: "https://raw.githubusercontent.com/owner/repo/" +
				"main/skills/hello/SKILL.md",
			want: gitHubLocation{
				Owner:   "owner",
				Repo:    "repo",
				Ref:     "main",
				DirPath: "skills/hello",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGitHubLocation(tc.raw)
			if err != nil {
				t.Fatalf("parseGitHubLocation() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestGitHubInstallerInstall(t *testing.T) {
	userRoot := t.TempDir()
	if err := os.MkdirAll(userRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	repo, err := skill.NewFSRepository(userRoot)
	if err != nil {
		t.Fatalf("NewFSRepository() error = %v", err)
	}

	server := newGitHubTestServer(t)
	defer server.Close()

	installer := &gitHubInstaller{
		userSkillsRoot: userRoot,
		repo:           repo,
		client:         server.Client(),
		apiBaseURL:     server.URL,
		webBaseURL:     server.URL,
	}

	resp, err := installer.install(context.Background(),
		gitHubInstallRequest{
			URL: "https://github.com/owner/repo/blob/main/" +
				"skills/hello/SKILL.md",
		})
	if err != nil {
		t.Fatalf("install() error = %v", err)
	}

	if got, want := resp.SkillName, "hello"; got != want {
		t.Fatalf("skill name = %q, want %q", got, want)
	}
	if !resp.Refreshed {
		t.Fatal("expected repo refresh to happen")
	}
	if !containsString(resp.InstalledFiles, "scripts/hello.sh") {
		t.Fatalf("installed files = %v", resp.InstalledFiles)
	}

	sk, err := repo.Get("hello")
	if err != nil {
		t.Fatalf("repo.Get() error = %v", err)
	}
	if got, want := sk.Summary.Description,
		"Write a hello file."; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}

	scriptPath := filepath.Join(resp.InstallDir, "scripts", "hello.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", scriptPath, err)
	}
	if got := info.Mode().Perm(); got != executableFileMode {
		t.Fatalf("mode = %v, want %v", got, executableFileMode)
	}
}

func TestGitHubInstallerInstall_FallbackToArchive(t *testing.T) {
	userRoot := t.TempDir()
	if err := os.MkdirAll(userRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	repo, err := skill.NewFSRepository(userRoot)
	if err != nil {
		t.Fatalf("NewFSRepository() error = %v", err)
	}

	server := newGitHubArchiveServer(t)
	defer server.Close()

	installer := &gitHubInstaller{
		userSkillsRoot: userRoot,
		repo:           repo,
		client:         server.Client(),
		apiBaseURL:     server.URL,
		webBaseURL:     server.URL,
	}

	resp, err := installer.install(context.Background(),
		gitHubInstallRequest{
			URL: "https://github.com/owner/repo/blob/main/" +
				"skills/hello/SKILL.md",
		})
	if err != nil {
		t.Fatalf("install() error = %v", err)
	}
	if got, want := resp.SkillName, "hello"; got != want {
		t.Fatalf("skill name = %q, want %q", got, want)
	}
	if !resp.Refreshed {
		t.Fatal("expected repo refresh to happen")
	}
	if !containsString(resp.InstalledFiles, "scripts/hello.sh") {
		t.Fatalf("installed files = %v", resp.InstalledFiles)
	}

	sk, err := repo.Get("hello")
	if err != nil {
		t.Fatalf("repo.Get() error = %v", err)
	}
	if got, want := sk.Summary.Description,
		"Archive install."; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}

	scriptPath := filepath.Join(resp.InstallDir, "scripts", "hello.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", scriptPath, err)
	}
	if got := info.Mode().Perm(); got != executableFileMode {
		t.Fatalf("mode = %v, want %v", got, executableFileMode)
	}
}

func TestExtractArchiveFile_RespectsRemainingBytes(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)

	file, err := writer.Create("repo-main/skills/hello/notes.txt")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := file.Write([]byte("123456")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reader, err := zip.NewReader(
		bytes.NewReader(buf.Bytes()),
		int64(buf.Len()),
	)
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}

	stats := &installStats{
		totalBytes: maxInstallBytes - 5,
	}
	destDir := t.TempDir()
	err = extractArchiveFile(
		reader.File[0],
		destDir,
		"notes.txt",
		stats,
	)
	if err == nil {
		t.Fatal("expected total size limit error")
	}
	want := "skill exceeds total size limit"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want substring %q", err, want)
	}
}

func TestReadInstalledSkillMeta_FallbackToDirName(t *testing.T) {
	tempDir := t.TempDir()
	skillPath := filepath.Join(tempDir, skillFileName)
	content := "---\ndescription: Hello demo\n---\nbody\n"
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	name, description, err := readInstalledSkillMeta(
		skillPath,
		"hello",
	)
	if err != nil {
		t.Fatalf("readInstalledSkillMeta() error = %v", err)
	}
	if got, want := name, "hello"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
	if got, want := description, "Hello demo"; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}

func newGitHubTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/contents/skills/hello", func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		items := []gitHubContentItem{
			{
				Type: "file",
				Name: skillFileName,
				Path: "skills/hello/" + skillFileName,
				DownloadURL: serverFileURL(
					r,
					"/raw/skills/hello/"+skillFileName,
				),
			},
			{
				Type: "dir",
				Name: "scripts",
				Path: "skills/hello/scripts",
			},
		}
		_ = json.NewEncoder(w).Encode(items)
	})
	mux.HandleFunc("/repos/owner/repo/contents/skills/hello/scripts",
		func(w http.ResponseWriter, r *http.Request) {
			items := []gitHubContentItem{
				{
					Type: "file",
					Name: "hello.sh",
					Path: "skills/hello/scripts/hello.sh",
					DownloadURL: serverFileURL(
						r,
						"/raw/skills/hello/scripts/hello.sh",
					),
				},
			}
			_ = json.NewEncoder(w).Encode(items)
		},
	)
	mux.HandleFunc("/raw/skills/hello/SKILL.md", func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		_, _ = w.Write([]byte(
			"---\nname: hello\ndescription: Write a hello " +
				"file.\n---\nbody\n",
		))
	})
	mux.HandleFunc("/raw/skills/hello/scripts/hello.sh", func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		_, _ = w.Write([]byte("#!/usr/bin/env bash\necho hi\n"))
	})

	return httptest.NewServer(mux)
}

func serverFileURL(r *http.Request, suffix string) string {
	return "http://" + r.Host + suffix
}

func newGitHubArchiveServer(t *testing.T) *httptest.Server {
	t.Helper()

	archiveBytes := buildGitHubArchive(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/contents/skills/hello", func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("rate limited"))
	})
	mux.HandleFunc("/owner/repo/archive/refs/heads/main.zip", func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		_, _ = w.Write(archiveBytes)
	})

	return httptest.NewServer(mux)
}

func buildGitHubArchive(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)

	writeArchiveFile := func(name string, body string) {
		t.Helper()
		file, err := writer.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) error = %v", name, err)
		}
		if _, err := file.Write([]byte(body)); err != nil {
			t.Fatalf("Write(%q) error = %v", name, err)
		}
	}

	writeArchiveFile(
		"repo-main/skills/hello/SKILL.md",
		"---\nname: hello\ndescription: Archive install.\n---\n",
	)
	writeArchiveFile(
		"repo-main/skills/hello/scripts/hello.sh",
		"#!/usr/bin/env bash\necho hi\n",
	)

	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return buf.Bytes()
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
