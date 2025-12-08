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

// mongoConnector is the function used to connect to MongoDB.
var mongoConnector = func(ctx context.Context, opts ...*options.ClientOptions) (*mongo.Client, error) {
	return mongo.Connect(ctx, opts...)
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
	client, err := mongoConnector(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("mongodb: connect failed: %w", err)
	}

	// Verify connection
	if err := client.Ping(ctx, nil); err != nil {
		client.Disconnect(ctx)
		return nil, fmt.Errorf("mongodb: ping failed: %w", err)
	}

	return newDefaultClient(client), nil
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
	InsertOne(ctx context.Context, database string, coll string, document any,
		opts ...*options.InsertOneOptions) (*mongo.InsertOneResult, error)

	// UpdateOne executes an update command to update at most one document in the collection.
	UpdateOne(ctx context.Context, database string, coll string, filter any, update any,
		opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)

	// DeleteOne executes a delete command to delete at most one document from the collection.
	DeleteOne(ctx context.Context, database string, coll string, filter any,
		opts ...*options.DeleteOptions) (*mongo.DeleteResult, error)

	// DeleteMany executes a delete command to delete documents from the collection.
	DeleteMany(ctx context.Context, database string, coll string, filter any,
		opts ...*options.DeleteOptions) (*mongo.DeleteResult, error)

	// FindOne executes a find command and returns a SingleResult for one document in the collection.
	FindOne(ctx context.Context, database string, coll string, filter any,
		opts ...*options.FindOneOptions) *mongo.SingleResult

	// Find executes a find command and returns a Cursor over the matching documents in the collection.
	Find(ctx context.Context, database string, coll string, filter any,
		opts ...*options.FindOptions) (*mongo.Cursor, error)

	// CountDocuments returns the number of documents in the collection.
	CountDocuments(ctx context.Context, database string, coll string, filter any,
		opts ...*options.CountOptions) (int64, error)

	// Transaction executes a transaction.
	// The sf parameter is a function that receives a mongo.SessionContext for transaction operations.
	Transaction(ctx context.Context, sf func(sc mongo.SessionContext) error, tOpts []*options.TransactionOptions,
		opts ...*options.SessionOptions) error

	// Disconnect closes the mongo client.
	Disconnect(ctx context.Context) error
}

// session defines the interface for MongoDB session operations.
type session interface {
	EndSession(ctx context.Context)
	WithTransaction(ctx context.Context, fn func(sc mongo.SessionContext) (any, error),
		opts ...*options.TransactionOptions) (any, error)
}

// defaultClient wraps *mongo.Client to implement the Client interface.
type defaultClient struct {
	client       *mongo.Client
	startSession func(opts ...*options.SessionOptions) (session, error)
}

// newDefaultClient creates a new defaultClient with the given mongo.Client.
func newDefaultClient(client *mongo.Client) *defaultClient {
	return &defaultClient{
		client: client,
		startSession: func(opts ...*options.SessionOptions) (session, error) {
			return client.StartSession(opts...)
		},
	}
}

// InsertOne implements Client.InsertOne.
func (c *defaultClient) InsertOne(ctx context.Context, database string, coll string, document any,
	opts ...*options.InsertOneOptions) (*mongo.InsertOneResult, error) {
	return c.client.Database(database).Collection(coll).InsertOne(ctx, document, opts...)
}

// UpdateOne implements Client.UpdateOne.
func (c *defaultClient) UpdateOne(ctx context.Context, database string, coll string, filter any,
	update any, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return c.client.Database(database).Collection(coll).UpdateOne(ctx, filter, update, opts...)
}

// DeleteOne implements Client.DeleteOne.
func (c *defaultClient) DeleteOne(ctx context.Context, database string, coll string, filter any,
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	return c.client.Database(database).Collection(coll).DeleteOne(ctx, filter, opts...)
}

// DeleteMany implements Client.DeleteMany.
func (c *defaultClient) DeleteMany(ctx context.Context, database string, coll string, filter any,
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	return c.client.Database(database).Collection(coll).DeleteMany(ctx, filter, opts...)
}

// FindOne implements Client.FindOne.
func (c *defaultClient) FindOne(ctx context.Context, database string, coll string, filter any,
	opts ...*options.FindOneOptions) *mongo.SingleResult {
	return c.client.Database(database).Collection(coll).FindOne(ctx, filter, opts...)
}

// Find implements Client.Find.
func (c *defaultClient) Find(ctx context.Context, database string, coll string, filter any,
	opts ...*options.FindOptions) (*mongo.Cursor, error) {
	return c.client.Database(database).Collection(coll).Find(ctx, filter, opts...)
}

// CountDocuments implements Client.CountDocuments.
func (c *defaultClient) CountDocuments(ctx context.Context, database string, coll string, filter any,
	opts ...*options.CountOptions) (int64, error) {
	return c.client.Database(database).Collection(coll).CountDocuments(ctx, filter, opts...)
}

// Transaction implements Client.Transaction.
func (c *defaultClient) Transaction(ctx context.Context, sf func(sc mongo.SessionContext) error,
	tOpts []*options.TransactionOptions, opts ...*options.SessionOptions) error {
	session, err := c.startSession(opts...)
	if err != nil {
		return fmt.Errorf("mongodb: start session failed: %w", err)
	}
	defer session.EndSession(ctx)

	var txOpt *options.TransactionOptions
	if len(tOpts) > 0 {
		txOpt = tOpts[0]
	}

	_, err = session.WithTransaction(ctx, func(sc mongo.SessionContext) (any, error) {
		return nil, sf(sc)
	}, txOpt)
	return err
}

// Disconnect implements Client.Disconnect.
func (c *defaultClient) Disconnect(ctx context.Context) error {
	return c.client.Disconnect(ctx)
}
