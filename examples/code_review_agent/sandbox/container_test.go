// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package sandbox

import (
	"testing"
)

func TestContainerSandbox_Name(t *testing.T) {
	// 不实际创建（需要 Docker），只测试接口
	sb := &ContainerSandbox{image: "golang:1.21-alpine"}
	if sb.Name() != "container" {
		t.Errorf("Name() = %q, 期望 %q", sb.Name(), "container")
	}
}

func TestContainerSandbox_DefaultImage(t *testing.T) {
	sb := &ContainerSandbox{}
	if sb.image != "" {
		t.Errorf("默认 image 应为空，实际 %q", sb.image)
	}
}

func TestCheckDockerAvailable(t *testing.T) {
	err := checkDockerAvailable()
	if err != nil {
		t.Logf("Docker 不可用（CI 环境可能没有）: %v", err)
		t.Skip("跳过：Docker 不可用")
	}
	t.Log("Docker 可用")
}
