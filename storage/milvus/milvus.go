//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package milvus

import (
	"context"
	"fmt"
	"time"

	client "github.com/milvus-io/milvus/client/v2/milvusclient"
	"google.golang.org/grpc"
)

// Client is the interface for the milvus client.
type Client interface {
	HasCollection(ctx context.Context, option client.HasCollectionOption, callOptions ...grpc.CallOption) (has bool, err error)
	CreateCollection(ctx context.Context, option client.CreateCollectionOption, callOptions ...grpc.CallOption) error
	LoadCollection(ctx context.Context, option client.LoadCollectionOption, callOptions ...grpc.CallOption) (client.LoadTask, error)
	Insert(ctx context.Context, option client.InsertOption, callOptions ...grpc.CallOption) (client.InsertResult, error)
	Upsert(ctx context.Context, option client.UpsertOption, callOptions ...grpc.CallOption) (client.UpsertResult, error)
	Query(ctx context.Context, option client.QueryOption, callOptions ...grpc.CallOption) (client.ResultSet, error)
	Delete(ctx context.Context, option client.DeleteOption, callOptions ...grpc.CallOption) (client.DeleteResult, error)
	Search(ctx context.Context, option client.SearchOption, callOptions ...grpc.CallOption) ([]client.ResultSet, error)
	HybridSearch(ctx context.Context, option client.HybridSearchOption, callOptions ...grpc.CallOption) ([]client.ResultSet, error)
	Close(ctx context.Context) error
}

func init() {
	milvusRegistry = make(map[string][]ClientBuilderOpt)
}

var milvusRegistry map[string][]ClientBuilderOpt

type clientBuilder func(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error)

var globalBuilder clientBuilder = defaultClientBuilder

// SetClientBuilder sets the postgres client builder.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the postgres client builder.
func GetClientBuilder() clientBuilder {
	return globalBuilder
}

// ClientBuilderOpt is the option for the milvus client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the milvus client.
type ClientBuilderOpts struct {
	// Address is the address of the milvus server.
	// Remote address, "localhost:19530".
	Address string
	// Username is the username of the milvus server.
	Username string
	// Password is the password of the milvus server.
	Password string
	// DBName is the name of the database, "default"
	DBName string
	// API key
	APIKey string
	// DialOptions is the dial options for the milvus server.
	DialOptions []grpc.DialOption
}

// WithAddress sets the address of the milvus server.
func WithAddress(address string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.Address = address
	}
}

// WithUsername sets the username of the milvus server.
func WithUsername(username string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.Username = username
	}
}

// WithPassword sets the password of the milvus server.
func WithPassword(password string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.Password = password
	}
}

// WithDBName sets the name of the database, "default"
func WithDBName(dbName string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.DBName = dbName
	}
}

// WithAPIKey sets the API key
func WithAPIKey(apiKey string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.APIKey = apiKey
	}
}

// WithDialOptions sets the dial options for the milvus server.
func WithDialOptions(opts ...grpc.DialOption) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.DialOptions = opts
	}
}

// defaultClientBuilder is the default client builder for milvus.
func defaultClientBuilder(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error) {
	opts := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(opts)
	}

	if opts.Address == "" {
		return nil, fmt.Errorf("milvus address is empty")
	}

	var cfg client.ClientConfig
	cfg.Address = opts.Address
	if opts.Username != "" {
		cfg.Username = opts.Username
	}
	if opts.Password != "" {
		cfg.Password = opts.Password
	}
	if opts.DBName != "" {
		cfg.DBName = opts.DBName
	}
	if opts.APIKey != "" {
		cfg.APIKey = opts.APIKey
	}
	if len(opts.DialOptions) > 0 {
		cfg.DialOptions = opts.DialOptions
	} else {
		cfg.DialOptions = []grpc.DialOption{grpc.WithTimeout(5 * time.Second)}
	}
	return client.New(ctx, &cfg)
}

// RegisterMilvusInstance registers a milvus instance options.
// If the instance already exists, it will be overwritten.
func RegisterMilvusInstance(name string, opts ...ClientBuilderOpt) {
	milvusRegistry[name] = opts
}

// GetMilvusInstance gets the milvus instance options.
func GetMilvusInstance(name string) ([]ClientBuilderOpt, bool) {
	instance, ok := milvusRegistry[name]
	return instance, ok
}
