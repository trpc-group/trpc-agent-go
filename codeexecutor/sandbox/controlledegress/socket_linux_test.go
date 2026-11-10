//go:build linux

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package controlledegress

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrepareUnixSocketPathPreservesBusyListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.sock")
	listener, err := unix.Socket(
		unix.AF_UNIX,
		unix.SOCK_STREAM|unix.SOCK_CLOEXEC,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(listener)
	if err := unix.Bind(listener, &unix.SockaddrUnix{Name: path}); err != nil {
		t.Fatal(err)
	}
	if err := unix.Listen(listener, 1); err != nil {
		t.Fatal(err)
	}
	var clients []int
	defer func() {
		for _, fd := range clients {
			_ = unix.Close(fd)
		}
	}()
	for i := 0; i < 16; i++ {
		fd, socketErr := unix.Socket(
			unix.AF_UNIX,
			unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK,
			0,
		)
		if socketErr != nil {
			t.Fatal(socketErr)
		}
		connectErr := unix.Connect(fd, &unix.SockaddrUnix{Name: path})
		if connectErr != nil {
			_ = unix.Close(fd)
			break
		}
		clients = append(clients, fd)
	}
	prepareErr := prepareUnixSocketPath(path)
	if prepareErr == nil {
		t.Fatal("busy socket path was accepted as stale")
	}
	_, statErr := os.Lstat(path)
	if statErr != nil {
		t.Fatalf("busy socket path was removed: %v", statErr)
	}
}
