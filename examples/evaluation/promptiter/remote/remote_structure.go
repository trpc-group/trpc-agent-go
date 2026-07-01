//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

func fetchRemoteStructure(ctx context.Context, appName string, target string, basePath string) (*astructure.Snapshot, error) {
	endpoint, err := url.JoinPath(target, basePath, appName, "structure")
	if err != nil {
		return nil, fmt.Errorf("build structure endpoint: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build structure request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch remote structure: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch remote structure returned %s", response.Status)
	}
	var payload struct {
		Structure *astructure.Snapshot `json:"structure"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode remote structure: %w", err)
	}
	if payload.Structure == nil {
		return nil, errors.New("remote structure response is empty")
	}
	return payload.Structure, nil
}
