//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	sandboxModuleTokenPrefix      = "m_"
	sandboxModuleTokenRandomBytes = 16
	sandboxModuleTokenLength      = len(sandboxModuleTokenPrefix) + 2*sandboxModuleTokenRandomBytes
	sandboxModuleBannerPrefix     = "==> trpc-agent-review-module-v1"
)

type sandboxModuleRecord struct {
	Path  string
	Token string
}

type sandboxModuleManifest struct {
	Records        []sandboxModuleRecord
	ModulesByToken map[string]string
}

func newSandboxModuleManifest(paths []string) (sandboxModuleManifest, error) {
	return newSandboxModuleManifestWithReader(paths, rand.Reader)
}

func newSandboxModuleManifestWithReader(
	paths []string,
	random io.Reader,
) (sandboxModuleManifest, error) {
	if random == nil {
		return sandboxModuleManifest{}, fmt.Errorf("sandbox module token source is nil")
	}
	manifest := sandboxModuleManifest{
		Records:        make([]sandboxModuleRecord, 0, len(paths)),
		ModulesByToken: make(map[string]string, len(paths)),
	}
	seenPaths := make(map[string]bool, len(paths))
	for _, module := range paths {
		if !isSafeSandboxModulePath(module) {
			return sandboxModuleManifest{}, fmt.Errorf("sandbox module path %q is invalid", module)
		}
		if seenPaths[module] {
			return sandboxModuleManifest{}, fmt.Errorf("sandbox module path %q is duplicated", module)
		}
		seenPaths[module] = true

		token, err := newSandboxModuleToken(random)
		if err != nil {
			return sandboxModuleManifest{}, err
		}
		if _, exists := manifest.ModulesByToken[token]; exists {
			return sandboxModuleManifest{}, fmt.Errorf("sandbox module token collision")
		}
		manifest.Records = append(manifest.Records, sandboxModuleRecord{
			Path:  module,
			Token: token,
		})
		manifest.ModulesByToken[token] = module
	}
	return manifest, nil
}

func newSandboxModuleToken(random io.Reader) (string, error) {
	data := make([]byte, sandboxModuleTokenRandomBytes)
	if _, err := io.ReadFull(random, data); err != nil {
		return "", fmt.Errorf("generate sandbox module token: %w", err)
	}
	return sandboxModuleTokenPrefix + hex.EncodeToString(data), nil
}

func isValidSandboxModuleToken(token string) bool {
	if len(token) != sandboxModuleTokenLength ||
		!strings.HasPrefix(token, sandboxModuleTokenPrefix) {
		return false
	}
	for _, char := range token[len(sandboxModuleTokenPrefix):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func isSafeSandboxModulePath(module string) bool {
	if module == "" || strings.ContainsRune(module, '\x00') {
		return false
	}
	normalized := strings.ReplaceAll(module, "\\", "/")
	if normalized != module {
		return false
	}
	if strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "//") ||
		hasWindowsDrive(normalized) {
		return false
	}
	clean := path.Clean(normalized)
	return clean == normalized && clean != ".." && !strings.HasPrefix(clean, "../")
}

func sandboxModuleBanner(mode string, token string) string {
	return sandboxModuleBannerPrefix + " " + mode + " " + token
}
