//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkipDefaultCredentialDenyOnlyExactGrant(t *testing.T) {
	home := t.TempDir()
	ssh := filepath.Join(home, ".ssh")
	config := filepath.Join(ssh, "config")

	if skipDefaultCredentialDeny(WorkspaceWriteProfile(), ssh) {
		t.Fatal("default profile should mask ~/.ssh")
	}
	if !skipDefaultCredentialDeny(WorkspaceWriteProfile().WithReadPaths(ssh), ssh) {
		t.Fatal("exact read grant should re-open ~/.ssh")
	}
	if skipDefaultCredentialDeny(WorkspaceWriteProfile().WithReadPaths(config), ssh) {
		t.Fatal("child read grant must not skip masking parent ~/.ssh")
	}
	if skipDefaultCredentialDeny(WorkspaceWriteProfile().WithReadPaths(home), ssh) {
		t.Fatal("parent $HOME grant should not re-open ~/.ssh")
	}
	if !skipDefaultCredentialDeny(WorkspaceWriteProfile().WithWritePaths(ssh), ssh) {
		t.Fatal("exact write grant should re-open ~/.ssh")
	}
	if skipDefaultCredentialDeny(WorkspaceWriteProfile().WithNoAccessPaths(ssh), ssh) {
		t.Fatal("no-access on ~/.ssh must not skip the default mask")
	}
	if skipDefaultCredentialDeny(WorkspaceWriteProfile().WithReadPaths("relative-ssh"), ssh) {
		t.Fatal("relative grant must not re-open an absolute credential path")
	}
	if !skipDefaultCredentialDeny(WorkspaceWriteProfile(), "") {
		t.Fatal("empty credential path should be skipped")
	}

	emptyPath := WorkspaceWriteProfile()
	emptyPath.fileSystem.Rules = append(emptyPath.fileSystem.Rules, fileSystemRule{
		Kind: rulePath, Access: accessRead, Path: "",
	})
	if skipDefaultCredentialDeny(emptyPath, ssh) {
		t.Fatal("empty path grant must not skip the default mask")
	}
}

func TestSkipGrantedHomeCredentialMask(t *testing.T) {
	home := t.TempDir()
	ssh := filepath.Join(home, ".ssh")
	config := filepath.Join(ssh, "config")

	if skipGrantedHomeCredentialMask(WorkspaceWriteProfile().WithReadMode(ReadModeHost), home, ssh) {
		t.Fatal("host-root profiles must keep home credential masks")
	}
	if skipGrantedHomeCredentialMask(WorkspaceWriteProfile().WithReadMode(ReadModeGranted), "", ssh) {
		t.Fatal("empty HOME should not skip an arbitrary credential path")
	}
	if skipGrantedHomeCredentialMask(WorkspaceWriteProfile().WithReadMode(ReadModeGranted), home, "/etc/shadow") {
		t.Fatal("granted mode must not skip non-home credential masks")
	}
	if !skipGrantedHomeCredentialMask(WorkspaceWriteProfile().WithReadMode(ReadModeGranted), home, ssh) {
		t.Fatal("granted mode without a parent grant should skip host ~/.ssh")
	}
	if skipGrantedHomeCredentialMask(WorkspaceWriteProfile().WithReadMode(ReadModeGranted).WithReadPaths(home), home, ssh) {
		t.Fatal("granted $HOME grant should keep the ~/.ssh mask")
	}
	if skipGrantedHomeCredentialMask(WorkspaceWriteProfile().WithReadMode(ReadModeGranted).WithWritePaths(home), home, ssh) {
		t.Fatal("granted $HOME write grant should keep the ~/.ssh mask")
	}
	if !skipGrantedHomeCredentialMask(WorkspaceWriteProfile().WithReadMode(ReadModeGranted).WithReadPaths(config), home, ssh) {
		t.Fatal("child grant should not treat ~/.ssh as visible")
	}
}

func TestDefaultCredentialDenyPathsIncludeHomeAndShadow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	paths := defaultCredentialDenyPaths()
	want := []string{
		"/etc/shadow",
		"/etc/gshadow",
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".aws"),
		filepath.Join(home, ".kube"),
		filepath.Join(home, ".config", "hub"),
		filepath.Join(home, ".cargo", "credentials.toml"),
		filepath.Join(home, ".terraform.d", "credentials.tfrc.json"),
	}
	for _, path := range want {
		if !containsString(paths, path) {
			t.Fatalf("defaultCredentialDenyPaths = %#v, missing %s", paths, path)
		}
	}
}

func TestSkipDefaultCredentialDenyWhenAbsFails(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := filepath.Abs("relative-cred"); err == nil {
		t.Skip("filepath.Abs does not fail after deleting the working directory")
	}
	if !skipDefaultCredentialDeny(WorkspaceWriteProfile(), "relative-cred") {
		t.Fatal("Abs failure should skip the default credential mask")
	}
}

func TestDefaultCredentialDenyPathsWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	paths := defaultCredentialDenyPaths()
	if !containsString(paths, "/etc/shadow") || !containsString(paths, "/etc/gshadow") {
		t.Fatalf("defaultCredentialDenyPaths = %#v, want system shadow paths", paths)
	}
	for _, path := range paths {
		if filepath.Base(path) == ".ssh" {
			t.Fatalf("defaultCredentialDenyPaths = %#v, unexpected home credential path", paths)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
