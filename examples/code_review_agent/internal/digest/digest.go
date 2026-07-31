//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package digest provides framed hashes for immutable review inputs.
package digest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
)

// New returns a SHA-256 hash for framed review digests.
func New() hash.Hash { return sha256.New() }

// Sum returns the hex digest accumulated by h.
func Sum(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

// String returns the SHA-256 hex digest of s.
func String(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// File returns a framed SHA-256 hex digest for one regular file.
func File(path string) (string, error) {
	file, err := OpenFile(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := New()
	if err := WriteOpenedFile(h, ".", file); err != nil {
		return "", err
	}
	return Sum(h), nil
}

// OpenFile opens path as a regular file for framed hashing.
func OpenFile(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !os.SameFile(before, after) || !after.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("not a regular file")
	}
	return file, nil
}

// WriteOpenedFile appends a framed record for file to h.
func WriteOpenedFile(h hash.Hash, rel string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return WriteFile(h, rel, info.Mode().Perm(), info.Size(), file)
}

// WriteFile appends a length-framed file record to h.
func WriteFile(h hash.Hash, rel string, mode os.FileMode, size int64, r io.Reader) error {
	if err := writeString(h, rel); err != nil {
		return err
	}
	var buf [16]byte
	binary.BigEndian.PutUint32(buf[:4], uint32(mode.Perm()))
	binary.BigEndian.PutUint64(buf[4:12], uint64(size))
	if _, err := h.Write(buf[:12]); err != nil {
		return err
	}
	_, err := io.Copy(h, r)
	return err
}

func writeString(h hash.Hash, s string) error {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	if _, err := h.Write(n[:]); err != nil {
		return err
	}
	_, err := h.Write([]byte(s))
	return err
}
