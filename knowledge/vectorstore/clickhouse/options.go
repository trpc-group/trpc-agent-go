//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package clickhouse provides a ClickHouse-backed vector store.
package clickhouse

import (
	"fmt"
	"math"
	"regexp"
)

// Default option values.
const (
	// defaultMaxResults is used when SearchQuery.Limit is not set.
	defaultMaxResults = 10
	// defaultVectorDimension is the default vector dimension.
	defaultVectorDimension = 1536
)

// Default column names. Callers may override them via the With*FieldName options.
const (
	defaultIDFieldName        = "id"
	defaultNameFieldName      = "name"
	defaultContentFieldName   = "content"
	defaultEmbeddingFieldName = "embedding"
	defaultMetadataFieldName  = "metadata"
	defaultCreatedAtFieldName = "created_at"
	defaultUpdatedAtFieldName = "updated_at"
)

// Metric selects the vector distance function used by similarity search.
type Metric int

const (
	// MetricCosine uses cosineDistance and maps distance to [0, 1] similarity.
	MetricCosine Metric = iota
	// MetricL2 uses L2Distance and maps distance to (0, 1] similarity.
	MetricL2
	// MetricInnerProduct uses dotProduct; scores are unbounded.
	MetricInnerProduct
)

// distanceFunction returns the ClickHouse distance function name for the metric.
func (m Metric) distanceFunction() string {
	switch m {
	case MetricL2:
		return "L2Distance"
	case MetricInnerProduct:
		return "dotProduct"
	default:
		return "cosineDistance"
	}
}

// toScore converts a raw distance/product value to a higher-is-more-similar
// score in [0, 1], matching the vectorstore.ScoredDocument.Score contract.
//
// SQL still orders by the raw value, so ranking is unaffected by this mapping.
func (m Metric) toScore(raw float64) float64 {
	switch m {
	case MetricL2:
		// L2 distance d in [0, +inf); convert to (0, 1] similarity.
		return 1.0 / (1.0 + raw)
	case MetricInnerProduct:
		// The dot product is unbounded in both directions, so it is squashed
		// with a logistic curve. The mapping is strictly increasing, which keeps
		// the ordering intact, and sends 0 to 0.5.
		return 1.0 / (1.0 + math.Exp(-raw))
	default:
		// cosineDistance is in [0, 2]; map to [0, 1] similarity.
		return 1.0 - raw/2
	}
}

// orderByDirection returns the ORDER BY direction for the raw metric value.
func (m Metric) orderByDirection() string {
	if m == MetricInnerProduct {
		// Larger inner product means more similar.
		return "DESC"
	}
	// Smaller distance means more similar for cosine and L2.
	return "ASC"
}

// FilterFieldType describes a filter field type declared in the table schema.
type FilterFieldType int32

const (
	// FilterFieldString maps to the ClickHouse String type.
	FilterFieldString FilterFieldType = 0
	// FilterFieldInt64 maps to the ClickHouse Int64 type.
	FilterFieldInt64 FilterFieldType = 1
	// FilterFieldFloat64 maps to the ClickHouse Float64 type.
	FilterFieldFloat64 FilterFieldType = 2
)

// clickhouseType returns the ClickHouse column type for the filter field type.
func (t FilterFieldType) clickhouseType() string {
	switch t {
	case FilterFieldInt64:
		return "Int64"
	case FilterFieldFloat64:
		return "Float64"
	default:
		return "String"
	}
}

// FilterFieldSpec declares a field that may be used in a WHERE filter. Each
// field is materialized as a dedicated column in the ClickHouse table so it can
// be filtered efficiently.
type FilterFieldSpec struct {
	// Name is the metadata key and the column name.
	Name string
	// Type determines the ClickHouse column type.
	Type FilterFieldType
}

// options contains all configurable vector store settings.
type options struct {
	// Table configuration.
	tableName       string // Required table name.
	vectorDimension int    // Must match the embedding dimension.
	metric          Metric // Vector distance metric.

	// Column names. Callers may override the default*FieldName values.
	idFieldName        string
	nameFieldName      string
	contentFieldName   string
	embeddingFieldName string
	metadataFieldName  string
	createdAtFieldName string
	updatedAtFieldName string

	// filterFields lists fields explicitly declared as filterable. Each field
	// is materialized as a dedicated typed column.
	filterFields []FilterFieldSpec

	// autoCreateTable creates a missing table when true. Disable it when the
	// table is provisioned externally.
	autoCreateTable bool

	// allowDestructiveDeleteAll permits DeleteByFilter with DeleteAll=true.
	allowDestructiveDeleteAll bool

	// Named instance registered through storage.RegisterClickHouseInstance.
	instanceName string

	// Connection settings. dsn has higher priority than instanceName.
	dsn          string
	extraOptions []any

	// Operation behavior.
	maxResults int // Default Search limit.
}

// defaultOptions contains values applied before With* options.
var defaultOptions = options{
	vectorDimension:    defaultVectorDimension,
	metric:             MetricCosine,
	idFieldName:        defaultIDFieldName,
	nameFieldName:      defaultNameFieldName,
	contentFieldName:   defaultContentFieldName,
	embeddingFieldName: defaultEmbeddingFieldName,
	metadataFieldName:  defaultMetadataFieldName,
	createdAtFieldName: defaultCreatedAtFieldName,
	updatedAtFieldName: defaultUpdatedAtFieldName,
	maxResults:         defaultMaxResults,
	autoCreateTable:    true,
}

// Option configures a VectorStore.
type Option func(*options)

// WithTableName sets the required ClickHouse table name.
func WithTableName(name string) Option { return func(o *options) { o.tableName = name } }

// WithVectorDimension sets the vector dimension, which must match the embedding output.
func WithVectorDimension(dim int) Option { return func(o *options) { o.vectorDimension = dim } }

// WithMetric sets the vector distance metric.
func WithMetric(m Metric) Option { return func(o *options) { o.metric = m } }

// WithFilterFields registers fields allowed in WHERE filters. The fields are
// materialized as dedicated typed columns and copied by name from
// document.Metadata during Add and Update.
func WithFilterFields(specs ...FilterFieldSpec) Option {
	return func(o *options) { o.filterFields = append(o.filterFields, specs...) }
}

// WithAutoCreateTable controls whether New creates a missing table. It defaults
// to true. When false, New skips table creation and does not probe for the
// table; the caller is responsible for provisioning the table before use.
func WithAutoCreateTable(enable bool) Option {
	return func(o *options) { o.autoCreateTable = enable }
}

// WithAllowDestructiveDeleteAll controls whether DeleteByFilter with
// DeleteAll=true may run. It is disabled by default and must be explicitly
// enabled because it clears the entire table.
func WithAllowDestructiveDeleteAll(enable bool) Option {
	return func(o *options) { o.allowDestructiveDeleteAll = enable }
}

// WithInstanceName selects a named client registered with
// storage.RegisterClickHouseInstance.
func WithInstanceName(name string) Option { return func(o *options) { o.instanceName = name } }

// WithDSN sets the ClickHouse connection DSN directly. It has higher priority
// than WithInstanceName.
func WithDSN(dsn string) Option { return func(o *options) { o.dsn = dsn } }

// WithExtraOptions passes options to a custom storage ClientBuilder.
func WithExtraOptions(opts ...any) Option {
	return func(o *options) { o.extraOptions = append(o.extraOptions, opts...) }
}

// WithMaxResults sets the default Search limit.
func WithMaxResults(n int) Option { return func(o *options) { o.maxResults = n } }

// Field-name overrides are intended for compatibility with existing tables.

// WithIDFieldName overrides the id column name.
func WithIDFieldName(s string) Option { return func(o *options) { o.idFieldName = s } }

// WithNameFieldName overrides the name column name.
func WithNameFieldName(s string) Option { return func(o *options) { o.nameFieldName = s } }

// WithContentFieldName overrides the content column name.
func WithContentFieldName(s string) Option { return func(o *options) { o.contentFieldName = s } }

// WithEmbeddingFieldName overrides the embedding column name.
func WithEmbeddingFieldName(s string) Option { return func(o *options) { o.embeddingFieldName = s } }

// WithMetadataFieldName overrides the metadata column name.
func WithMetadataFieldName(s string) Option { return func(o *options) { o.metadataFieldName = s } }

// WithCreatedAtFieldName overrides the created_at column name.
func WithCreatedAtFieldName(s string) Option {
	return func(o *options) { o.createdAtFieldName = s }
}

// WithUpdatedAtFieldName overrides the updated_at column name.
func WithUpdatedAtFieldName(s string) Option {
	return func(o *options) { o.updatedAtFieldName = s }
}

// safeIdentifier restricts field/column names to a conservative ASCII identifier subset.
var safeIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateOptions validates construction-time settings before they reach runtime operations.
func validateOptions(o *options) error {
	if o.tableName == "" {
		return errTableNameRequired
	}
	if !safeIdentifier.MatchString(o.tableName) {
		return fmt.Errorf("clickhouse: table name %q is not a valid identifier", o.tableName)
	}
	if o.vectorDimension <= 0 {
		return fmt.Errorf("clickhouse: vectorDimension must be > 0, got %d", o.vectorDimension)
	}
	if o.maxResults <= 0 {
		return fmt.Errorf("clickhouse: maxResults must be > 0, got %d", o.maxResults)
	}
	// Reject unknown metrics instead of silently falling back to cosine, which
	// would rank results by a metric the caller did not ask for.
	switch o.metric {
	case MetricCosine, MetricL2, MetricInnerProduct:
	default:
		return fmt.Errorf("clickhouse: metric %d is not a supported Metric", o.metric)
	}

	// Validate built-in column names.
	builtin := map[string]string{
		"id":         o.idFieldName,
		"name":       o.nameFieldName,
		"content":    o.contentFieldName,
		"embedding":  o.embeddingFieldName,
		"metadata":   o.metadataFieldName,
		"created_at": o.createdAtFieldName,
		"updated_at": o.updatedAtFieldName,
	}
	seen := make(map[string]string, len(builtin)+len(o.filterFields))
	for label, name := range builtin {
		if name == "" {
			return fmt.Errorf("clickhouse: %s field name must not be empty", label)
		}
		if !safeIdentifier.MatchString(name) {
			return fmt.Errorf("clickhouse: %s field name %q is not a valid identifier", label, name)
		}
		if other, dup := seen[name]; dup {
			return fmt.Errorf("clickhouse: built-in field name %q is used by both %s and %s; they must differ",
				name, other, label)
		}
		seen[name] = label
	}

	// Validate filterFields.
	for i, spec := range o.filterFields {
		if spec.Name == "" {
			return fmt.Errorf("clickhouse: filterFields[%d].Name must not be empty", i)
		}
		if !safeIdentifier.MatchString(spec.Name) {
			return fmt.Errorf("clickhouse: filterFields[%d].Name %q is not a valid identifier", i, spec.Name)
		}
		if other, dup := seen[spec.Name]; dup {
			return fmt.Errorf("clickhouse: filterFields[%d].Name %q conflicts with %s; choose a different name",
				i, spec.Name, other)
		}
		seen[spec.Name] = "filter field"
		switch spec.Type {
		case FilterFieldString, FilterFieldInt64, FilterFieldFloat64:
		default:
			return fmt.Errorf("clickhouse: filterFields[%d].Type %d is not a supported FilterFieldType for %q",
				i, spec.Type, spec.Name)
		}
	}
	return nil
}
