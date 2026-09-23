//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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
	// defaultMaxUpdateRecords bounds how many records one UpdateByFilter call
	// may rewrite. Every match is buffered before the write, so the bound caps
	// the memory, the statement text, and the argument list of a single INSERT.
	defaultMaxUpdateRecords = 1000
)

// Default column names. Callers may override them via the WithIDField,
// WithNameField, WithContentField, WithEmbeddingField, WithMetadataField,
// WithCreatedAtField, and WithUpdatedAtField options.
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
	// MetricInnerProduct ranks by dotProduct. The raw product is unbounded in
	// both directions, but search results report it through the same [0, 1]
	// Score contract as the other metrics, via the monotonic mapping in
	// Metric.toScore. Ordering follows the raw product.
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
//
// The column is Nullable so that a document without the field can be stored as
// NULL and distinguished from one that explicitly set the zero value.
func (t FilterFieldType) clickhouseType() string {
	switch t {
	case FilterFieldInt64:
		return "Nullable(Int64)"
	case FilterFieldFloat64:
		return "Nullable(Float64)"
	default:
		return "Nullable(String)"
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

	// syncMutations makes delete mutations wait for completion before the
	// call returns, so a following read observes the deletion.
	syncMutations bool

	// Named instance registered through storage.RegisterClickHouseInstance.
	instanceName string

	// Connection settings. dsn has higher priority than instanceName.
	dsn          string
	extraOptions []any

	// Operation behavior.
	maxResults int // Default Search limit.

	// maxUpdateRecords bounds a single UpdateByFilter rewrite. Zero or a negative
	// value removes the bound, which lets a wide filter grow without limit.
	maxUpdateRecords int
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
	maxUpdateRecords:   defaultMaxUpdateRecords,
	autoCreateTable:    true,
	syncMutations:      true,
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

// WithMaxUpdateRecords caps how many records one UpdateByFilter call may match.
// It defaults to 1000. A call whose filter matches more records fails before any
// write instead of buffering them all.
//
// The bound exists because this store rewrites every match as one multi-row
// INSERT: the matched records are held in memory until that statement runs, and
// they also determine its text length and argument count.
//
// Pass zero or a negative value to remove the bound. Do that only when the
// caller already controls how wide the filter can be.
func WithMaxUpdateRecords(n int) Option { return func(o *options) { o.maxUpdateRecords = n } }

// WithSynchronousMutations controls whether delete mutations wait for
// completion before Delete, DeleteByFilter, and DeleteAll return. It defaults
// to true so a read issued right after a delete observes the deletion.
//
// Disable it only when the caller accepts that a deleted document can remain
// visible for a short time, for example to keep bulk deletions off the critical
// path. A failed asynchronous mutation cannot be reported through the returned
// error.
func WithSynchronousMutations(enable bool) Option {
	return func(o *options) { o.syncMutations = enable }
}

// WithInstanceName selects a named client registered with
// storage.RegisterClickHouseInstance.
func WithInstanceName(name string) Option { return func(o *options) { o.instanceName = name } }

// WithDSN sets the ClickHouse connection DSN directly. It has higher priority
// than WithInstanceName.
func WithDSN(dsn string) Option { return func(o *options) { o.dsn = dsn } }

// WithExtraOptions passes extra options through to the ClickHouse client
// builder from trpc-agent-go/storage/clickhouse. They are appended to the
// options resolved from WithDSN or WithInstanceName and are meant for builders
// registered through storage.SetClientBuilder that accept their own values.
//
// The values are opaque here: they are forwarded verbatim and are not
// validated, so a builder that does not recognize one may fail at connect
// time. Callers that only need a preconfigured client can pass these options
// once when registering a named instance and select it with WithInstanceName.
func WithExtraOptions(opts ...any) Option {
	return func(o *options) { o.extraOptions = append(o.extraOptions, opts...) }
}

// WithMaxResults sets the default Search limit.
func WithMaxResults(n int) Option { return func(o *options) { o.maxResults = n } }

// Field-name overrides are intended for compatibility with existing tables.

// WithIDField overrides the id column name.
func WithIDField(s string) Option { return func(o *options) { o.idFieldName = s } }

// WithNameField overrides the name column name.
func WithNameField(s string) Option { return func(o *options) { o.nameFieldName = s } }

// WithContentField overrides the content column name.
func WithContentField(s string) Option { return func(o *options) { o.contentFieldName = s } }

// WithEmbeddingField overrides the embedding column name.
func WithEmbeddingField(s string) Option { return func(o *options) { o.embeddingFieldName = s } }

// WithMetadataField overrides the metadata column name.
func WithMetadataField(s string) Option { return func(o *options) { o.metadataFieldName = s } }

// WithCreatedAtField overrides the created_at column name.
func WithCreatedAtField(s string) Option {
	return func(o *options) { o.createdAtFieldName = s }
}

// WithUpdatedAtField overrides the updated_at column name.
func WithUpdatedAtField(s string) Option {
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
		return fmt.Errorf("clickhouse: metric %d is not a supported metric", o.metric)
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
