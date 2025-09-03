//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides Elasticsearch client interface, implementation and options.
package elasticsearch

import (
	"fmt"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	esv8 "github.com/elastic/go-elasticsearch/v8"
	esv9 "github.com/elastic/go-elasticsearch/v9"
)

// defaultClientBuilder selects implementation by Version and builds a client.
func defaultClientBuilder(builderOpts ...ClientBuilderOpt) (any, error) {
	o := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(o)
	}

	switch o.Version {
	case ESVersionV7:
		return newClientV7(o)
	case ESVersionV8:
		return newClientV8(o)
	case ESVersionV9, ESVersionUnspecified:
		return newClientV9(o)
	default:
		return nil, fmt.Errorf("elasticsearch: unknown version %s", o.Version)
	}
}

// newClientV7 builds a v7 client from generic builder options.
func newClientV7(o *ClientBuilderOpts) (*esv7.Client, error) {
	cfg := esv7.Config{
		Addresses:              o.Addresses,
		Username:               o.Username,
		Password:               o.Password,
		APIKey:                 o.APIKey,
		CertificateFingerprint: o.CertificateFingerprint,
		CompressRequestBody:    o.CompressRequestBody,
		EnableMetrics:          o.EnableMetrics,
		EnableDebugLogger:      o.EnableDebugLogger,
		RetryOnStatus:          o.RetryOnStatus,
		MaxRetries:             o.MaxRetries,
	}
	cli, err := esv7.NewClient(cfg)
	return cli, err
}

// newClientV8 builds a v8 client from generic builder options.
func newClientV8(o *ClientBuilderOpts) (*esv8.Client, error) {
	cfg := esv8.Config{
		Addresses:              o.Addresses,
		Username:               o.Username,
		Password:               o.Password,
		APIKey:                 o.APIKey,
		CertificateFingerprint: o.CertificateFingerprint,
		CompressRequestBody:    o.CompressRequestBody,
		EnableMetrics:          o.EnableMetrics,
		EnableDebugLogger:      o.EnableDebugLogger,
		RetryOnStatus:          o.RetryOnStatus,
		MaxRetries:             o.MaxRetries,
	}
	cli, err := esv8.NewClient(cfg)
	return cli, err
}

// newClientV9 builds a v9 client from generic builder options.
func newClientV9(o *ClientBuilderOpts) (*esv9.Client, error) {
	cfg := esv9.Config{
		Addresses:              o.Addresses,
		Username:               o.Username,
		Password:               o.Password,
		APIKey:                 o.APIKey,
		CertificateFingerprint: o.CertificateFingerprint,
		CompressRequestBody:    o.CompressRequestBody,
		EnableMetrics:          o.EnableMetrics,
		EnableDebugLogger:      o.EnableDebugLogger,
		RetryOnStatus:          o.RetryOnStatus,
		MaxRetries:             o.MaxRetries,
	}
	cli, err := esv9.NewClient(cfg)
	return cli, err
}
