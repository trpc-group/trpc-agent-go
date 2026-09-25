//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build linux || freebsd || netbsd || openbsd || dragonfly

package opensandbox

import "syscall"

// dirOpenFlag is the open(2) flag that makes the kernel reject opens
// of non-directory paths (ENOTDIR). On these platforms that is
// O_DIRECTORY.
const dirOpenFlag = syscall.O_DIRECTORY
