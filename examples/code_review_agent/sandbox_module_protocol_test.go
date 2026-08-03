//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const (
	testRootModuleToken   = "m_00000000000000000000000000000000"
	testNestedModuleToken = "m_11111111111111111111111111111111"
	testOtherModuleToken  = "m_22222222222222222222222222222222"
)

func testRootDiagnosticModules() map[string]string {
	return map[string]string{testRootModuleToken: "."}
}

func sandboxModulePaths(manifest sandboxModuleManifest) []string {
	paths := make([]string, 0, len(manifest.Records))
	for _, record := range manifest.Records {
		paths = append(paths, record.Path)
	}
	return paths
}

func sandboxModuleTokenForPath(manifest sandboxModuleManifest, module string) (string, bool) {
	for _, record := range manifest.Records {
		if record.Path == module {
			return record.Token, true
		}
	}
	return "", false
}

func testSandboxModuleManifestBytes(manifest sandboxModuleManifest) []byte {
	var data bytes.Buffer
	for _, record := range manifest.Records {
		data.WriteString(record.Path)
		data.WriteByte(0)
		data.WriteString(record.Token)
		data.WriteByte(0)
	}
	return data.Bytes()
}

func TestNewSandboxModuleManifestUsesRandomTokens(t *testing.T) {
	random := append(bytes.Repeat([]byte{0x01}, sandboxModuleTokenRandomBytes),
		bytes.Repeat([]byte{0xab}, sandboxModuleTokenRandomBytes)...)
	manifest, err := newSandboxModuleManifestWithReader(
		[]string{".", "nested\nmodule"},
		bytes.NewReader(random),
	)
	if err != nil {
		t.Fatal(err)
	}
	wantRecords := []sandboxModuleRecord{
		{Path: ".", Token: "m_01010101010101010101010101010101"},
		{Path: "nested\nmodule", Token: "m_abababababababababababababababab"},
	}
	if !reflect.DeepEqual(manifest.Records, wantRecords) {
		t.Fatalf("records = %#v, want %#v", manifest.Records, wantRecords)
	}
	for _, record := range wantRecords {
		if !isValidSandboxModuleToken(record.Token) ||
			manifest.ModulesByToken[record.Token] != record.Path {
			t.Fatalf("manifest token mapping = %#v", manifest)
		}
	}
}

func TestNewSandboxModuleManifestFailsClosed(t *testing.T) {
	t.Run("random source", func(t *testing.T) {
		_, err := newSandboxModuleManifestWithReader(
			[]string{"."},
			sandboxModuleErrorReader{},
		)
		if err == nil || !strings.Contains(err.Error(), "generate sandbox module token") {
			t.Fatalf("manifest error = %v, want random source failure", err)
		}
	})

	t.Run("token collision", func(t *testing.T) {
		_, err := newSandboxModuleManifestWithReader(
			[]string{".", "nested"},
			bytes.NewReader(make([]byte, 2*sandboxModuleTokenRandomBytes)),
		)
		if err == nil || !strings.Contains(err.Error(), "token collision") {
			t.Fatalf("manifest error = %v, want collision failure", err)
		}
	})

	t.Run("duplicate path", func(t *testing.T) {
		_, err := newSandboxModuleManifestWithReader(
			[]string{".", "."},
			bytes.NewReader(make([]byte, 2*sandboxModuleTokenRandomBytes)),
		)
		if err == nil || !strings.Contains(err.Error(), "duplicated") {
			t.Fatalf("manifest error = %v, want duplicate path failure", err)
		}
	})
}

func TestSandboxModuleTokenValidation(t *testing.T) {
	for _, token := range []string{
		testRootModuleToken,
		"m_abcdefabcdefabcdefabcdefabcdefab",
	} {
		if !isValidSandboxModuleToken(token) {
			t.Fatalf("token %q was rejected", token)
		}
	}
	for _, token := range []string{
		"",
		"m_0000000000000000000000000000000",
		"m_000000000000000000000000000000000",
		"m_0000000000000000000000000000000g",
		"m_0000000000000000000000000000000A",
		"x_00000000000000000000000000000000",
	} {
		if isValidSandboxModuleToken(token) {
			t.Fatalf("invalid token %q was accepted", token)
		}
	}
}

func TestSandboxModuleTokenSurvivesSanitization(t *testing.T) {
	banner := sandboxModuleBanner("vet", testRootModuleToken)
	run, redactions := sanitizeSandboxRun(sandboxRun{
		Stdout: banner,
		Stderr: banner,
	})
	if redactions != 0 || run.Stdout != banner || run.Stderr != banner {
		t.Fatalf("sanitized run = %+v, redactions = %d", run, redactions)
	}
}

func TestSandboxModulePathValidation(t *testing.T) {
	for _, module := range []string{
		".", "nested", "with space", "nested\nmodule", "nested\rmodule", " ",
	} {
		if !isSafeSandboxModulePath(module) {
			t.Fatalf("safe module path %q was rejected", module)
		}
	}
	for _, module := range []string{
		"",
		"../escape",
		"nested/../escape",
		"./nested",
		"nested\\module",
		"/absolute",
		"C:/absolute",
		"nested\x00module",
	} {
		if isSafeSandboxModulePath(module) {
			t.Fatalf("unsafe module path %q was accepted", module)
		}
	}
}

type sandboxModuleErrorReader struct{}

func (sandboxModuleErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("random unavailable")
}
