// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package safety

import (
	"testing"
)

func TestMaskSensitiveInfo_Password(t *testing.T) {
	input := "password=secret123456"
	got := MaskSensitiveInfo(input)
	if got == input {
		t.Errorf("应脱敏，但未变化: %q", got)
	}
	if !containsREDACTED(got) {
		t.Errorf("应包含 REDACTED: %q", got)
	}
}

func TestMaskSensitiveInfo_AWSKey(t *testing.T) {
	input := "using key AKIAIOSFODNN7EXAMPLE for auth"
	got := MaskSensitiveInfo(input)
	if got == input {
		t.Errorf("应脱敏 AWS Key: %q", got)
	}
}

func TestMaskSensitiveInfo_GitHubToken(t *testing.T) {
	input := "token: ghp_FAKEtokenForTestingPurposes0000000000"
	got := MaskSensitiveInfo(input)
	if got == input {
		t.Errorf("应脱敏 GitHub Token: %q", got)
	}
}

func TestMaskSensitiveInfo_StripeKey(t *testing.T) {
	input := "key := sk_test_fakeTestKeyNotReal12345678"
	got := MaskSensitiveInfo(input)
	if got == input {
		t.Errorf("应脱敏 Stripe Key: %q", got)
	}
}

func TestMaskSensitiveInfo_JWTToken(t *testing.T) {
	input := "tok := eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123def456ghi789"
	got := MaskSensitiveInfo(input)
	if got == input {
		t.Errorf("应脱敏 JWT Token: %q", got)
	}
}

func TestMaskSensitiveInfo_PrivateKey(t *testing.T) {
	input := "pem := -----BEGIN RSA PRIVATE KEY-----"
	got := MaskSensitiveInfo(input)
	if got == input {
		t.Errorf("应脱敏私钥: %q", got)
	}
}

func TestMaskSensitiveInfo_DBConnectionString(t *testing.T) {
	input := `dsn := "postgres://admin:s3cret@localhost:5432/mydb"`
	got := MaskSensitiveInfo(input)
	if got == input {
		t.Errorf("应脱敏数据库连接串: %q", got)
	}
	// 验证密码被替换但保留协议和主机
	if !containsREDACTED(got) {
		t.Errorf("应包含 REDACTED: %q", got)
	}
}

func TestMaskSensitiveInfo_URLCredentials(t *testing.T) {
	input := `url := "https://admin:pass123@db.example.com/api"`
	got := MaskSensitiveInfo(input)
	if got == input {
		t.Errorf("应脱敏 URL 凭据: %q", got)
	}
}

func TestMaskSensitiveInfo_NormalText(t *testing.T) {
	input := "hello world, this is normal text"
	got := MaskSensitiveInfo(input)
	if got != input {
		t.Errorf("正常文本不应被修改: %q → %q", input, got)
	}
}

func TestMaskSensitiveInfo_TokenizerNotMistaken(t *testing.T) {
	input := "using tokenizer library"
	got := MaskSensitiveInfo(input)
	if got != input {
		t.Errorf("tokenizer 不应被误脱敏: %q → %q", input, got)
	}
}

func TestMaskSensitiveInfo_MultiLine(t *testing.T) {
	input := "line1: normal\npassword=secret123456\nline3: normal"
	got := MaskSensitiveInfo(input)
	// 第 2 行应被脱敏
	if got == input {
		t.Errorf("多行文本中密码应被脱敏")
	}
}

func containsREDACTED(s string) bool {
	return len(s) >= 8 && (contains(s, "REDACTED") || contains(s, "REMOVED"))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
