//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mongodb provides the MongoDB instance info management.
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
// It creates a native MongoDB client using the official Go driver.
func defaultClientBuilder(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error) {
	o := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(o)
	}

	if o.URI == "" {
		return nil, errors.New("mongodb: URI is empty")
	}

	// Create MongoDB client options
	clientOpts := options.Client().ApplyURI(o.URI)

	// Connect to MongoDB
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("mongodb: connect failed: %w", err)
	}

	// Verify connection
	if err := client.Ping(ctx, nil); err != nil {
		client.Disconnect(ctx)
		return nil, fmt.Errorf("mongodb: ping failed: %w", err)
	}

	return &nativeClient{client: client}, nil
}

// ClientBuilderOpt is the option for the mongodb client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the mongodb client.
type ClientBuilderOpts struct {
	// URI is the mongodb connection string.
	// Format: "mongodb://username:password@host:port/database?options"
	URI string

	// ExtraOptions is the extra options for the mongodb client.
	// This is mainly used for customized mongodb client builders.
	ExtraOptions []any
}

// WithClientBuilderDSN sets the mongodb connection URI for clientBuilder.
func WithClientBuilderDSN(uri string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.URI = uri
	}
}

// WithExtraOptions sets the mongodb client extra options for clientBuilder.
// This option is mainly used for customized mongodb client builders.
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
// This is a subset of the internal mongodb.Client interface,
// containing only the methods needed by the session layer.
type Client interface {
	// InsertOne executes an insert command to insert a single document into the collection.
	InsertOne(ctx context.Context, database string, coll string, document interface{},
		opts ...*options.InsertOneOptions) (*mongo.InsertOneResult, error)

	// UpdateOne executes an update command to update at most one document in the collection.
	UpdateOne(ctx context.Context, database string, coll string, filter interface{}, update interface{},
		opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)

	// DeleteOne executes a delete command to delete at most one document from the collection.
	DeleteOne(ctx context.Context, database string, coll string, filter interface{},
		opts ...*options.DeleteOptions) (*mongo.DeleteResult, error)

	// DeleteMany executes a delete command to delete documents from the collection.
	DeleteMany(ctx context.Context, database string, coll string, filter interface{},
		opts ...*options.DeleteOptions) (*mongo.DeleteResult, error)

	// FindOne executes a find command and returns a SingleResult for one document in the collection.
	FindOne(ctx context.Context, database string, coll string, filter interface{},
		opts ...*options.FindOneOptions) *mongo.SingleResult

	// Find executes a find command and returns a Cursor over the matching documents in the collection.
	Find(ctx context.Context, database string, coll string, filter interface{},
		opts ...*options.FindOptions) (*mongo.Cursor, error)

	// CountDocuments returns the number of documents in the collection.
	CountDocuments(ctx context.Context, database string, coll string, filter interface{},
		opts ...*options.CountOptions) (int64, error)

	// Transaction executes a transaction.
	// The sf parameter is a function that receives a mongo.SessionContext for transaction operations.
	Transaction(ctx context.Context, sf func(sc mongo.SessionContext) error, tOpts []*options.TransactionOptions,
		opts ...*options.SessionOptions) error

	// Disconnect closes the mongo client.
	Disconnect(ctx context.Context) error
}

// ErrNoClientBuilder is returned when no client builder is set.
var ErrNoClientBuilder = errors.New("mongodb: no client builder set, please call SetClientBuilder first")

// NewClient creates a new mongodb client using the global builder.
func NewClient(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
	if globalBuilder == nil {
		return nil, ErrNoClientBuilder
	}
	return globalBuilder(ctx, opts...)
}

// NewClientFromInstance creates a new mongodb client from a registered instance.
func NewClientFromInstance(ctx context.Context, instanceName string, extraOpts ...ClientBuilderOpt) (Client, error) {
	if globalBuilder == nil {
		return nil, ErrNoClientBuilder
	}

	builderOpts, ok := GetMongoDBInstance(instanceName)
	if !ok {
		return nil, errors.New("mongodb: instance not found: " + instanceName)
	}

	// Append extra options if provided
	allOpts := make([]ClientBuilderOpt, 0, len(builderOpts)+len(extraOpts))
	allOpts = append(allOpts, builderOpts...)
	allOpts = append(allOpts, extraOpts...)

	return globalBuilder(ctx, allOpts...)
}

// nativeClient wraps *mongo.Client to implement the Client interface.
type nativeClient struct {
	client *mongo.Client
}

// InsertOne implements Client.InsertOne.
func (c *nativeClient) InsertOne(ctx context.Context, database string, coll string, document interface{},
	opts ...*options.InsertOneOptions) (*mongo.InsertOneResult, error) {
	return c.client.Database(database).Collection(coll).InsertOne(ctx, document, opts...)
}

// UpdateOne implements Client.UpdateOne.
func (c *nativeClient) UpdateOne(ctx context.Context, database string, coll string, filter interface{},
	update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return c.client.Database(database).Collection(coll).UpdateOne(ctx, filter, update, opts...)
}

// DeleteOne implements Client.DeleteOne.
func (c *nativeClient) DeleteOne(ctx context.Context, database string, coll string, filter interface{},
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	return c.client.Database(database).Collection(coll).DeleteOne(ctx, filter, opts...)
}

// DeleteMany implements Client.DeleteMany.
func (c *nativeClient) DeleteMany(ctx context.Context, database string, coll string, filter interface{},
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	return c.client.Database(database).Collection(coll).DeleteMany(ctx, filter, opts...)
}

// FindOne implements Client.FindOne.
func (c *nativeClient) FindOne(ctx context.Context, database string, coll string, filter interface{},
	opts ...*options.FindOneOptions) *mongo.SingleResult {
	return c.client.Database(database).Collection(coll).FindOne(ctx, filter, opts...)
}

// Find implements Client.Find.
func (c *nativeClient) Find(ctx context.Context, database string, coll string, filter interface{},
	opts ...*options.FindOptions) (*mongo.Cursor, error) {
	return c.client.Database(database).Collection(coll).Find(ctx, filter, opts...)
}

// CountDocuments implements Client.CountDocuments.
func (c *nativeClient) CountDocuments(ctx context.Context, database string, coll string, filter interface{},
	opts ...*options.CountOptions) (int64, error) {
	return c.client.Database(database).Collection(coll).CountDocuments(ctx, filter, opts...)
}

// Transaction implements Client.Transaction.
func (c *nativeClient) Transaction(ctx context.Context, sf func(sc mongo.SessionContext) error,
	tOpts []*options.TransactionOptions, opts ...*options.SessionOptions) error {
	session, err := c.client.StartSession(opts...)
	if err != nil {
		return fmt.Errorf("mongodb: start session failed: %w", err)
	}
	defer session.EndSession(ctx)

	var txOpt *options.TransactionOptions
	if len(tOpts) > 0 {
		txOpt = tOpts[0]
	}

	_, err = session.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
		return nil, sf(sc)
	}, txOpt)
	return err
}

// Disconnect implements Client.Disconnect.
func (c *nativeClient) Disconnect(ctx context.Context) error {
	return c.client.Disconnect(ctx)
}
