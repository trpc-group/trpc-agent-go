// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package checkers provides built-in safety checkers for the Tool Execution
// Safety Guard. Each checker implements the toolsafety.Checker interface and
// focuses on one risk dimension: dangerous commands, network egress, shell
// bypass, resource abuse, sensitive leaks, and host execution risks.
package checkers
