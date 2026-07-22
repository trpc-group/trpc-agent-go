//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"
)

const (
	moduleProxyTarget       = "/opt/trpc-agent/modproxy"
	maxGoSumBytes           = 2 << 20
	maxProxyModules         = 512
	maxProxyBytes     int64 = 128 << 20
	maxProxyEntries         = 100_000
	maxProxyExpanded  int64 = 256 << 20
	moduleCacheLookup       = 5 * time.Second
)

// ErrDependencyCache classifies a missing or unsafe offline dependency input.
var ErrDependencyCache = errors.New("sandbox dependency cache failure")

type moduleProxy struct {
	Path          string
	Digest        string
	Modules       int
	Bytes         int64
	Entries       int
	ExpandedBytes int64
}

type moduleVersion struct {
	path, version, sum, modSum string
}

func buildModuleProxy(ctx context.Context, repoPath, moduleCache string) (result moduleProxy, resultErr error) {
	versions, err := readModuleVersions(repoPath)
	if err != nil {
		return result, err
	}
	root, err := os.MkdirTemp("", ".cr-modproxy-")
	if err != nil {
		return result, dependencyError("create snapshot", err)
	}
	result.Path = root
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, os.RemoveAll(root))
		}
	}()
	if len(versions) == 0 {
		result.Digest = emptyProxyDigest()
		if err := makeProxyReadOnly(root); err != nil {
			return result, dependencyError("lock snapshot", err)
		}
		return result, nil
	}
	if moduleCache == "" {
		moduleCache, err = resolveModuleCache(ctx)
		if err != nil {
			return result, err
		}
	}
	downloadRoot, err := trustedDownloadRoot(moduleCache)
	if err != nil {
		return result, err
	}
	digest := sha256.New()
	for _, version := range versions {
		if err := ctx.Err(); err != nil {
			return result, dependencyError("build snapshot", err)
		}
		bytesCopied, expanded, entries, copyErr := copyModuleVersion(ctx, downloadRoot, root, version, digest,
			result.Bytes, result.ExpandedBytes, result.Entries)
		if copyErr != nil {
			return result, copyErr
		}
		result.Bytes += bytesCopied
		result.ExpandedBytes += expanded
		result.Entries += entries
		result.Modules++
	}
	result.Digest = hex.EncodeToString(digest.Sum(nil))
	if err := makeProxyReadOnly(root); err != nil {
		return result, dependencyError("lock snapshot", err)
	}
	return result, nil
}

func readModuleVersions(repoPath string) ([]moduleVersion, error) {
	sumPath := filepath.Join(repoPath, "go.sum")
	info, err := os.Lstat(sumPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, dependencyError("inspect go.sum", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxGoSumBytes {
		return nil, dependencyError("inspect go.sum", errors.New("go.sum is not a bounded regular file"))
	}
	file, err := os.Open(sumPath)
	if err != nil {
		return nil, dependencyError("open go.sum", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, dependencyError("verify go.sum", errors.Join(err, errors.New("go.sum changed while opening")))
	}
	selected := make(map[string]moduleVersion)
	scanner := bufio.NewScanner(io.LimitReader(file, maxGoSumBytes+1))
	scanner.Buffer(make([]byte, 4096), maxGoSumBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, dependencyError("parse go.sum", fmt.Errorf("malformed line %q", line))
		}
		version := strings.TrimSuffix(fields[1], "/go.mod")
		isMod := version != fields[1]
		if !strings.HasPrefix(fields[2], "h1:") || module.Check(fields[0], version) != nil {
			return nil, dependencyError("parse go.sum", fmt.Errorf("invalid module entry %q", scanner.Text()))
		}
		key := fields[0] + "\x00" + version
		entry := selected[key]
		entry.path, entry.version = fields[0], version
		if isMod {
			if entry.modSum != "" && entry.modSum != fields[2] {
				return nil, dependencyError("parse go.sum", errors.New("conflicting go.mod sums"))
			}
			entry.modSum = fields[2]
		} else {
			if entry.sum != "" && entry.sum != fields[2] {
				return nil, dependencyError("parse go.sum", errors.New("conflicting module sums"))
			}
			entry.sum = fields[2]
		}
		selected[key] = entry
		if len(selected) > maxProxyModules {
			return nil, dependencyError("parse go.sum", errors.New("module count exceeds limit"))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, dependencyError("read go.sum", err)
	}
	versions := make([]moduleVersion, 0, len(selected))
	for _, version := range selected {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].path < versions[j].path ||
			versions[i].path == versions[j].path && versions[i].version < versions[j].version
	})
	return versions, nil
}

func resolveModuleCache(ctx context.Context) (string, error) {
	if configured := os.Getenv("GOMODCACHE"); configured != "" {
		return configured, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, moduleCacheLookup)
	defer cancel()
	command := exec.CommandContext(lookupCtx, "go", "env", "GOMODCACHE")
	output, err := command.Output()
	if err != nil {
		return "", dependencyError("resolve host module cache", err)
	}
	cache := strings.TrimSpace(string(output))
	if cache == "" || !filepath.IsAbs(cache) {
		return "", dependencyError("resolve host module cache", errors.New("module cache path is not absolute"))
	}
	return cache, nil
}

func trustedDownloadRoot(moduleCache string) (string, error) {
	root := filepath.Join(moduleCache, "cache", "download")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", dependencyError("resolve module download cache", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", dependencyError("inspect module download cache", errors.Join(err, errors.New("download cache is not a directory")))
	}
	return resolved, nil
}

func copyModuleVersion(ctx context.Context, downloadRoot, destination string, version moduleVersion, digest hash.Hash,
	alreadyCopied, alreadyExpanded int64, alreadyEntries int) (int64, int64, int, error) {
	escapedPath, err := module.EscapePath(version.path)
	if err != nil {
		return 0, 0, 0, dependencyError("escape module path", err)
	}
	escapedVersion, err := module.EscapeVersion(version.version)
	if err != nil {
		return 0, 0, 0, dependencyError("escape module version", err)
	}
	sourceDir := filepath.Join(downloadRoot, filepath.FromSlash(escapedPath), "@v")
	destinationDir := filepath.Join(destination, filepath.FromSlash(escapedPath), "@v")
	if err := os.MkdirAll(destinationDir, 0o700); err != nil {
		return 0, 0, 0, dependencyError("create module snapshot directory", err)
	}
	if version.sum != "" {
		hashPath := filepath.Join(sourceDir, escapedVersion+".ziphash")
		hashContent, hashErr := readTrustedFile(downloadRoot, hashPath, 256)
		if hashErr != nil || strings.TrimSpace(string(hashContent)) != version.sum {
			return 0, 0, 0, dependencyError("verify module zip hash", errors.Join(hashErr, errors.New("zip hash differs from go.sum")))
		}
	}
	suffixes := make([]string, 0, 3)
	infoPath := filepath.Join(sourceDir, escapedVersion+".info")
	if version.sum != "" {
		suffixes = append(suffixes, ".info")
	} else if _, infoErr := os.Lstat(infoPath); infoErr == nil {
		suffixes = append(suffixes, ".info")
	} else if !os.IsNotExist(infoErr) {
		return 0, 0, 0, dependencyError("inspect optional module info", infoErr)
	}
	if version.modSum != "" {
		suffixes = append(suffixes, ".mod")
	}
	if version.sum != "" {
		suffixes = append(suffixes, ".zip")
	}
	var copied int64
	var expanded int64
	var entries int
	for _, suffix := range suffixes {
		if err := ctx.Err(); err != nil {
			return 0, 0, 0, dependencyError("build snapshot", err)
		}
		source := filepath.Join(sourceDir, escapedVersion+suffix)
		limit := maxProxyBytes - alreadyCopied - copied
		content, readErr := readTrustedFile(downloadRoot, source, limit)
		if readErr != nil {
			return 0, 0, 0, dependencyError("read module cache entry", readErr)
		}
		if suffix == ".info" {
			var info struct{ Version string }
			if jsonErr := json.Unmarshal(content, &info); jsonErr != nil || info.Version != version.version {
				return 0, 0, 0, dependencyError("validate module info", errors.Join(jsonErr, errors.New("info version mismatch")))
			}
		} else if suffix == ".mod" {
			modHash, hashErr := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(content)), nil
			})
			if hashErr != nil || modHash != version.modSum {
				return 0, 0, 0, dependencyError("verify module go.mod hash", errors.Join(hashErr, errors.New("go.mod hash differs from go.sum")))
			}
		}
		relative := filepath.ToSlash(filepath.Join(escapedPath, "@v", escapedVersion+suffix))
		destinationPath := filepath.Join(destinationDir, escapedVersion+suffix)
		if err := writeExclusive(destinationPath, content); err != nil {
			return 0, 0, 0, dependencyError("write module snapshot", err)
		}
		if suffix == ".zip" {
			if _, checkErr := modzip.CheckZip(module.Version{Path: version.path, Version: version.version}, destinationPath); checkErr != nil {
				return 0, 0, 0, dependencyError("validate module zip", checkErr)
			}
			zipExpanded, zipEntries, boundsErr := moduleZipBounds(content)
			if boundsErr != nil || alreadyExpanded+expanded+zipExpanded > maxProxyExpanded ||
				alreadyEntries+entries+zipEntries > maxProxyEntries {
				return 0, 0, 0, dependencyError("validate module zip bounds", errors.Join(boundsErr, errors.New("snapshot expanded size or entries exceed limit")))
			}
			expanded += zipExpanded
			entries += zipEntries
			if err := ctx.Err(); err != nil {
				return 0, 0, 0, dependencyError("build snapshot", err)
			}
			zipHash, hashErr := hashModuleZip(content)
			if hashErr != nil || zipHash != version.sum {
				return 0, 0, 0, dependencyError("verify module zip content", errors.Join(hashErr, errors.New("zip content differs from go.sum")))
			}
		}
		writeDigest(digest, relative, content)
		copied += int64(len(content))
	}
	return copied, expanded, entries, nil
}

func readTrustedFile(root, name string, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, errors.New("module snapshot exceeds byte limit")
	}
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.Join(err, errors.New("cache entry is not a bounded regular file"))
	}
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil || !withinRoot(root, resolved) {
		return nil, errors.Join(err, errors.New("cache entry escapes download root"))
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, errors.Join(err, errors.New("cache entry changed while opening"))
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(content)) > limit {
		return nil, errors.Join(err, errors.New("cache entry exceeds byte limit"))
	}
	return content, nil
}

func hashModuleZip(content []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", err
	}
	entries := make(map[string]*zip.File, len(archive.File))
	files := make([]string, 0, len(archive.File))
	for _, entry := range archive.File {
		if _, exists := entries[entry.Name]; exists {
			return "", fmt.Errorf("duplicate zip entry %q", entry.Name)
		}
		entries[entry.Name] = entry
		files = append(files, entry.Name)
	}
	return dirhash.Hash1(files, func(name string) (io.ReadCloser, error) {
		return entries[name].Open()
	})
}

func moduleZipBounds(content []byte) (int64, int, error) {
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return 0, 0, err
	}
	if len(archive.File) > maxProxyEntries {
		return 0, 0, errors.New("zip entry count exceeds limit")
	}
	var expanded int64
	for _, entry := range archive.File {
		if entry.UncompressedSize64 > uint64(maxProxyExpanded) || expanded > maxProxyExpanded-int64(entry.UncompressedSize64) {
			return 0, 0, errors.New("expanded zip exceeds limit")
		}
		expanded += int64(entry.UncompressedSize64)
	}
	return expanded, len(archive.File), nil
}

func writeExclusive(name string, content []byte) error {
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func makeProxyReadOnly(root string) error {
	return filepath.Walk(root, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(name, 0o555)
		}
		return os.Chmod(name, 0o444)
	})
}

func (p moduleProxy) Close() error {
	if p.Path == "" {
		return nil
	}
	if err := filepath.Walk(p.Path, func(name string, _ os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return os.Chmod(name, 0o700)
	}); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.RemoveAll(p.Path)
}

func dependencyError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrDependencyCache, operation, err)
}

func emptyProxyDigest() string {
	digest := sha256.Sum256(nil)
	return hex.EncodeToString(digest[:])
}

func writeDigest(digest hash.Hash, name string, content []byte) {
	_, _ = io.WriteString(digest, name)
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(content)
	_, _ = digest.Write([]byte{0})
}

func withinRoot(root, name string) bool {
	relative, err := filepath.Rel(root, name)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
