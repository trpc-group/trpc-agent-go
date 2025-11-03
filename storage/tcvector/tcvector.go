//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tcvector provides the tcvectordb instance info management.
package tcvector

import (
	"errors"

	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
)

func init() {
	tcvectorRegistry = make(map[string][]ClientBuilderOpt)
}

var tcvectorRegistry map[string][]ClientBuilderOpt

// ClientInterface is the interface for the tcvectordb client.
type ClientInterface interface {
	// DatabaseInterface is the interface for database operations.
	// Such as create database, collection, index, etc.
	tcvectordb.DatabaseInterface

	// FlatInterface is the interface for data operations.
	// Such as insert, update, delete, query, etc.
	tcvectordb.FlatInterface
}

type clientBuilder func(builderOpts ...ClientBuilderOpt) (ClientInterface, error)

// clientBuilder is the function to build the global tcvectordb client.
var globalBuilder clientBuilder = defaultClientBuilder

// SetClientBuilder sets the client builder for tcvectordb.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the tcvectordb client builder.
func GetClientBuilder() clientBuilder {
	return globalBuilder
}

// defaultClientBuilder is the default client builder for tcvectordb.
func defaultClientBuilder(builderOpts ...ClientBuilderOpt) (ClientInterface, error) {
	opts := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(opts)
	}

	// Validate required parameters
	if opts.HTTPURL == "" {
		return nil, errors.New("HTTPURL is required")
	}
	if opts.UserName == "" {
		return nil, errors.New("UserName is required")
	}
	if opts.Key == "" {
		return nil, errors.New("Key is required")
	}

	return tcvectordb.NewClient(opts.HTTPURL, opts.UserName, opts.Key, nil)
}

// ClientBuilderOpt is the option for the tcvectordb client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the tcvectordb client.
type ClientBuilderOpts struct {
	// HTTPURL is the http url for the tcvectordb client.
	HTTPURL string
	// UserName is the username for the tcvectordb client.
	UserName string
	// Key is the key for the tcvectordb client.
	Key string
}

// WithClientBuilderHTTPURL sets the http url for the tcvectordb client.
func WithClientBuilderHTTPURL(httpURL string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.HTTPURL = httpURL
	}
}

// WithClientBuilderUserName sets the username for the tcvectordb client.
func WithClientBuilderUserName(userName string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.UserName = userName
	}
}

// WithClientBuilderKey sets the key for the tcvectordb client.
func WithClientBuilderKey(key string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.Key = key
	}
}

// RegisterTcVectorInstance registers a tcvectordb instance options.
// If the instance already exists, it will be overwritten.
func RegisterTcVectorInstance(name string, opts ...ClientBuilderOpt) {
	tcvectorRegistry[name] = opts
}

// GetTcVectorInstance gets the tcvectordb instance options.
func GetTcVectorInstance(name string) ([]ClientBuilderOpt, bool) {
	instance, ok := tcvectorRegistry[name]
	return instance, ok
}
