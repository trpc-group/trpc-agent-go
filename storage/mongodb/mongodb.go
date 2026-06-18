//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mongodb provides the MongoDB instance info management and client interface.
package mongodb

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func init() {
	mongodbRegistry = make(map[string][]ClientBuilderOpt)
}

var mongodbRegistry map[string][]ClientBuilderOpt

// clientBuilder is the function type for building Client instances.
type clientBuilder func(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error)

var globalBuilder clientBuilder = defaultClientBuilder

// SetClientBuilder sets the mongodb client builder.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the mongodb client builder.
func GetClientBuilder() clientBuilder {
	return globalBuilder
}

// defaultClientBuilder is the default mongodb client builder.
// It connects with the official Go driver and verifies the connection before returning.
func defaultClientBuilder(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error) {
	o := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(o)
	}

	if o.URI == "" {
		return nil, errors.New("mongodb: uri is empty")
	}

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(o.URI))
	if err != nil {
		return nil, fmt.Errorf("mongodb: connect: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("mongodb: ping: %w", err)
	}

	return &defaultClient{client: client}, nil
}

// ClientBuilderOpt is the option for the mongodb client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the mongodb client.
type ClientBuilderOpts struct {
	// URI is the mongodb connection string.
	// Format: mongodb://[username:password@]host1[:port1][,...hostN[:portN]][/[defaultauthdb][?options]]
	// Example: mongodb://user:pass@localhost:27017/?replicaSet=rs0
	URI string

	// ExtraOptions is the extra options for the mongodb client.
	// This option is mainly used for customized mongodb client builders;
	// it is passed through verbatim and ignored by the default builder.
	ExtraOptions []any
}

// WithClientBuilderURI sets the mongodb connection URI for clientBuilder.
func WithClientBuilderURI(uri string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.URI = uri
	}
}

// WithExtraOptions sets the mongodb client extra options for clientBuilder.
// This option is mainly used for customized mongodb client builders, it will
// be passed to the builder.
func WithExtraOptions(extraOptions ...any) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ExtraOptions = append(opts.ExtraOptions, extraOptions...)
	}
}

// RegisterMongoDBInstance registers a mongodb instance with the given options.
func RegisterMongoDBInstance(name string, opts ...ClientBuilderOpt) {
	mongodbRegistry[name] = append(mongodbRegistry[name], opts...)
}

// GetMongoDBInstance gets the mongodb instance options by name.
func GetMongoDBInstance(name string) ([]ClientBuilderOpt, bool) {
	instance, ok := mongodbRegistry[name]
	return instance, ok
}

// Client defines the interface for MongoDB operations.
// It abstracts the common MongoDB operations needed by upstream packages
// (such as session/mongodb), making it easier to inject mock implementations for testing.
type Client interface {
	InsertOne(ctx context.Context, database, coll string, document any,
		opts ...*options.InsertOneOptions) (*mongo.InsertOneResult, error)

	UpdateOne(ctx context.Context, database, coll string, filter, update any,
		opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)

	UpdateMany(ctx context.Context, database, coll string, filter, update any,
		opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)

	DeleteOne(ctx context.Context, database, coll string, filter any,
		opts ...*options.DeleteOptions) (*mongo.DeleteResult, error)

	DeleteMany(ctx context.Context, database, coll string, filter any,
		opts ...*options.DeleteOptions) (*mongo.DeleteResult, error)

	FindOne(ctx context.Context, database, coll string, filter any,
		opts ...*options.FindOneOptions) *mongo.SingleResult

	// Find returns a cursor over documents matching the filter.
	// Callers must close the returned cursor when done.
	Find(ctx context.Context, database, coll string, filter any,
		opts ...*options.FindOptions) (*mongo.Cursor, error)

	// EnsureIndexes creates the given indexes on the collection if they do not exist.
	// Index creation is idempotent: existing indexes with matching keys and options
	// are left unchanged.
	EnsureIndexes(ctx context.Context, database, coll string,
		models []mongo.IndexModel, opts ...*options.CreateIndexesOptions) ([]string, error)

	// Transaction executes fn within a multi-document transaction.
	// Note: MongoDB transactions require a replica set or sharded cluster deployment;
	// they are not supported on standalone servers.
	Transaction(ctx context.Context, fn TxFunc, opts ...TxOption) error

	// Close terminates all connections to the MongoDB deployment.
	// After calling Close, the client should not be used anymore.
	Close(ctx context.Context) error
}

// TxFunc is a user transaction function.
// Return nil to commit, or any error to rollback.
type TxFunc func(sc mongo.SessionContext) error

// TxOption configures transaction options.
type TxOption func(*TxOptions)

// TxOptions are the configurable options of a transaction.
type TxOptions struct {
	// Transaction holds the per-transaction options. May be nil.
	Transaction *options.TransactionOptions
	// Session holds the per-session options. May be nil.
	Session *options.SessionOptions
}

// WithTransactionOptions sets the per-transaction options.
func WithTransactionOptions(o *options.TransactionOptions) TxOption {
	return func(opts *TxOptions) {
		opts.Transaction = o
	}
}

// WithSessionOptions sets the per-session options.
func WithSessionOptions(o *options.SessionOptions) TxOption {
	return func(opts *TxOptions) {
		opts.Session = o
	}
}

// defaultClient wraps *mongo.Client to implement the Client interface.
type defaultClient struct {
	client *mongo.Client
}

func (c *defaultClient) coll(database, coll string) *mongo.Collection {
	return c.client.Database(database).Collection(coll)
}

func (c *defaultClient) InsertOne(ctx context.Context, database, coll string, document any,
	opts ...*options.InsertOneOptions) (*mongo.InsertOneResult, error) {
	return c.coll(database, coll).InsertOne(ctx, document, opts...)
}

func (c *defaultClient) UpdateOne(ctx context.Context, database, coll string, filter, update any,
	opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return c.coll(database, coll).UpdateOne(ctx, filter, update, opts...)
}

func (c *defaultClient) UpdateMany(ctx context.Context, database, coll string, filter, update any,
	opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return c.coll(database, coll).UpdateMany(ctx, filter, update, opts...)
}

func (c *defaultClient) DeleteOne(ctx context.Context, database, coll string, filter any,
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	return c.coll(database, coll).DeleteOne(ctx, filter, opts...)
}

func (c *defaultClient) DeleteMany(ctx context.Context, database, coll string, filter any,
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	return c.coll(database, coll).DeleteMany(ctx, filter, opts...)
}

func (c *defaultClient) FindOne(ctx context.Context, database, coll string, filter any,
	opts ...*options.FindOneOptions) *mongo.SingleResult {
	return c.coll(database, coll).FindOne(ctx, filter, opts...)
}

func (c *defaultClient) Find(ctx context.Context, database, coll string, filter any,
	opts ...*options.FindOptions) (*mongo.Cursor, error) {
	return c.coll(database, coll).Find(ctx, filter, opts...)
}

func (c *defaultClient) EnsureIndexes(ctx context.Context, database, coll string,
	models []mongo.IndexModel, opts ...*options.CreateIndexesOptions) ([]string, error) {
	if len(models) == 0 {
		return nil, nil
	}
	return c.coll(database, coll).Indexes().CreateMany(ctx, models, opts...)
}

// Transaction starts a session, executes fn inside session.WithTransaction
// (which handles commit, rollback and transient-error retries internally),
// and ends the session.
func (c *defaultClient) Transaction(ctx context.Context, fn TxFunc, opts ...TxOption) error {
	txOpts := &TxOptions{}
	for _, opt := range opts {
		opt(txOpts)
	}

	var sessOpts []*options.SessionOptions
	if txOpts.Session != nil {
		sessOpts = append(sessOpts, txOpts.Session)
	}

	sess, err := c.client.StartSession(sessOpts...)
	if err != nil {
		return fmt.Errorf("mongodb: start session: %w", err)
	}
	defer sess.EndSession(ctx)

	var txOptsList []*options.TransactionOptions
	if txOpts.Transaction != nil {
		txOptsList = append(txOptsList, txOpts.Transaction)
	}

	_, err = sess.WithTransaction(ctx, func(sc mongo.SessionContext) (any, error) {
		return nil, fn(sc)
	}, txOptsList...)
	return err
}

func (c *defaultClient) Close(ctx context.Context) error {
	return c.client.Disconnect(ctx)
}
