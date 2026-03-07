//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	BaseURLEnvName         = "OPENCLAW_TELEGRAM_BASE_URL"
	telegramBaseURLEnvName = BaseURLEnvName

	errDefaultTransportType = "telegram: default transport is not http.Transport"
)

type ClientNetOptions struct {
	ProxyURL   string
	Timeout    time.Duration
	MaxRetries int
}

func BuildClientOptionsFromEnv(cfg ClientNetOptions) ([]Option, error) {
	opts := make([]Option, 0, 3)
	opts = append(opts, WithMaxRetries(cfg.MaxRetries))

	baseURL := strings.TrimSpace(os.Getenv(telegramBaseURLEnvName))
	if baseURL != "" {
		opts = append(opts, WithBaseURL(baseURL))
	}

	client, err := BuildHTTPClient(cfg.ProxyURL, cfg.Timeout)
	if err != nil {
		return nil, err
	}
	if client != nil {
		opts = append(opts, WithHTTPClient(client))
	}
	return opts, nil
}

func BuildHTTPClient(
	rawProxyURL string,
	httpTimeout time.Duration,
) (*http.Client, error) {
	proxyURL := strings.TrimSpace(rawProxyURL)
	if proxyURL == "" && httpTimeout <= 0 {
		return nil, nil
	}

	client := &http.Client{}
	if httpTimeout > 0 {
		client.Timeout = httpTimeout
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New(errDefaultTransportType)
	}

	cloned := transport.Clone()
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		cloned.Proxy = http.ProxyURL(u)
	}
	client.Transport = cloned
	return client, nil
}
