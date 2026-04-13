//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/admin"
)

const (
	adminIdentityFileName = "IDENTITY.md"

	adminIdentityFilePerm = 0o600
	adminIdentityDirPerm  = 0o700

	adminAssistantNameMaxRunes = 32

	adminIdentityFallbackRuntime = "runtime product"

	adminDefaultNameSourceFile = "Default name from IDENTITY.md"
	adminDefaultNameSourceApp  = "Default name from runtime product"

	adminChatsHelpText = "" +
		"This runtime does not keep a separate current name per " +
		"chat yet. Every chat uses the default name shown on the " +
		"Identity page."

	adminIdentityTrimCutset = "" +
		"\"'“”‘’<>《》「」『』【】()（）[]"
)

type adminIdentityProvider struct {
	filePath       string
	runtimeProduct string
}

type adminChatsProvider struct {
	identity *adminIdentityProvider
}

func buildAdminIdentityProvider(
	stateDir string,
	runtimeProduct string,
) *adminIdentityProvider {
	return &adminIdentityProvider{
		filePath: filepath.Join(
			strings.TrimSpace(stateDir),
			adminIdentityFileName,
		),
		runtimeProduct: defaultAdminRuntimeProduct(runtimeProduct),
	}
}

func buildAdminChatsProvider(
	identity *adminIdentityProvider,
) *adminChatsProvider {
	if identity == nil {
		return nil
	}
	return &adminChatsProvider{identity: identity}
}

func defaultAdminRuntimeProduct(raw string) string {
	product := strings.TrimSpace(raw)
	if product != "" {
		return product
	}
	return appName
}

func normalizeAdminAssistantName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	trimmed = strings.Trim(trimmed, adminIdentityTrimCutset)
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return ""
	}

	fields := strings.Fields(trimmed)
	if len(fields) != 0 {
		trimmed = strings.Join(fields, " ")
	}

	runes := []rune(trimmed)
	if len(runes) > adminAssistantNameMaxRunes {
		runes = runes[:adminAssistantNameMaxRunes]
	}
	return strings.TrimSpace(string(runes))
}

func readAdminAssistantName(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return normalizeAdminAssistantName(string(data)), nil
}

func writeAdminAssistantName(
	path string,
	name string,
) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("admin identity path is unavailable")
	}
	if err := os.MkdirAll(
		filepath.Dir(path),
		adminIdentityDirPerm,
	); err != nil {
		return err
	}

	body := ""
	name = normalizeAdminAssistantName(name)
	if name != "" {
		body = name + "\n"
	}
	return os.WriteFile(
		path,
		[]byte(body),
		adminIdentityFilePerm,
	)
}

func identityDefaultNameSource(
	status admin.IdentityStatus,
) string {
	if strings.TrimSpace(status.ConfiguredName) != "" {
		return adminDefaultNameSourceFile
	}
	return adminDefaultNameSourceApp
}

func (p *adminIdentityProvider) IdentityStatus() (
	admin.IdentityStatus,
	error,
) {
	if p == nil {
		return admin.IdentityStatus{}, nil
	}

	configured, err := readAdminAssistantName(p.filePath)
	if err != nil {
		return admin.IdentityStatus{}, err
	}

	effective := configured
	fallbackSource := ""
	if effective == "" {
		effective = p.runtimeProduct
		fallbackSource = adminIdentityFallbackRuntime
	}

	return admin.IdentityStatus{
		Enabled:        true,
		ConfiguredName: configured,
		EffectiveName:  effective,
		RuntimeProduct: p.runtimeProduct,
		SourcePath:     strings.TrimSpace(p.filePath),
		FallbackSource: fallbackSource,
	}, nil
}

func (p *adminIdentityProvider) SaveAssistantName(name string) error {
	if p == nil {
		return fmt.Errorf("identity provider is unavailable")
	}
	return writeAdminAssistantName(p.filePath, name)
}

func (p *adminChatsProvider) ChatsStatus() (
	admin.ChatsStatus,
	error,
) {
	if p == nil {
		return admin.ChatsStatus{}, nil
	}

	identity, err := p.identity.IdentityStatus()
	if err != nil {
		return admin.ChatsStatus{}, err
	}

	return admin.ChatsStatus{
		Enabled:               true,
		GlobalAssistantName:   strings.TrimSpace(identity.EffectiveName),
		RuntimeAssistantName:  strings.TrimSpace(identity.RuntimeProduct),
		GlobalAssistantSource: identityDefaultNameSource(identity),
		ChatOverrideHelp:      adminChatsHelpText,
	}, nil
}
