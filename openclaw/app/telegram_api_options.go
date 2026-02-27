//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

type telegramAPIOption = tgapi.Option

const telegramBaseURLEnvName = "OPENCLAW_TELEGRAM_BASE_URL"

func makeTelegramAPIOptions(
	rawProxyURL string,
	httpTimeout time.Duration,
	maxRetries int,
) ([]telegramAPIOption, error) {
	opts := make([]telegramAPIOption, 0, 3)
	opts = append(opts, tgapi.WithMaxRetries(maxRetries))

	baseURL := strings.TrimSpace(os.Getenv(telegramBaseURLEnvName))
	if baseURL != "" {
		opts = append(opts, tgapi.WithBaseURL(baseURL))
	}

	client, err := makeTelegramHTTPClient(rawProxyURL, httpTimeout)
	if err != nil {
		return nil, err
	}
	if client != nil {
		opts = append(opts, tgapi.WithHTTPClient(client))
	}
	return opts, nil
}

func makeTelegramHTTPClient(
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
		return nil, errors.New("telegram: default transport is not http.Transport")
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
