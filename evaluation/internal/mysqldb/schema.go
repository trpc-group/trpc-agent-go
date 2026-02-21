//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysqldb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

const (
	// TableNameEvalSets is the base table name for evaluation sets.
	TableNameEvalSets = "evaluation_eval_sets"
	// TableNameEvalCases is the base table name for evaluation cases.
	TableNameEvalCases = "evaluation_eval_cases"
	// TableNameMetrics is the base table name for evaluation metrics.
	TableNameMetrics = "evaluation_metrics"
	// TableNameEvalSetResults is the base table name for evaluation results.
	TableNameEvalSetResults = "evaluation_eval_set_results"
)

// Tables holds fully qualified table names with the configured prefix applied.
type Tables struct {
	EvalSets       string
	EvalCases      string
	Metrics        string
	EvalSetResults string
}

type tableDefinition struct {
	name     string
	template string
}

type indexDefinition struct {
	table    string
	name     string
	template string
}

type indexSpec struct {
	name     string
	template string
}

type schemaSpec struct {
	target    SchemaTarget
	tableName func(Tables) string
	tableSQL  string
	indexes   []indexSpec
}

var schemaSpecs = []schemaSpec{
	{
		target:    SchemaEvalSets,
		tableName: func(t Tables) string { return t.EvalSets },
		tableSQL:  sqlCreateEvalSetsTable,
		indexes: []indexSpec{
			{name: "uniq_eval_sets_app_eval_set", template: sqlCreateEvalSetsUniqueIndex},
			{name: "idx_eval_sets_app_created", template: sqlCreateEvalSetsAppCreatedIndex},
		},
	},
	{
		target:    SchemaEvalCases,
		tableName: func(t Tables) string { return t.EvalCases },
		tableSQL:  sqlCreateEvalCasesTable,
		indexes: []indexSpec{
			{name: "uniq_eval_cases_app_set_case", template: sqlCreateEvalCasesUniqueIndex},
			{name: "idx_eval_cases_app_set_order", template: sqlCreateEvalCasesOrderIndex},
		},
	},
	{
		target:    SchemaMetrics,
		tableName: func(t Tables) string { return t.Metrics },
		tableSQL:  sqlCreateMetricsTable,
		indexes: []indexSpec{
			{name: "uniq_metrics_app_set_name", template: sqlCreateMetricsUniqueIndex},
			{name: "idx_metrics_app_set", template: sqlCreateMetricsAppSetIndex},
		},
	},
	{
		target:    SchemaEvalSetResults,
		tableName: func(t Tables) string { return t.EvalSetResults },
		tableSQL:  sqlCreateEvalSetResultsTable,
		indexes: []indexSpec{
			{name: "uniq_results_app_result_id", template: sqlCreateEvalSetResultsUniqueIndex},
			{name: "idx_results_app_created", template: sqlCreateEvalSetResultsAppCreatedIndex},
			{name: "idx_results_app_set_created", template: sqlCreateEvalSetResultsAppSetCreatedIndex},
		},
	},
}

// SchemaTarget selects which evaluation tables should be ensured.
type SchemaTarget uint8

const (
	// SchemaEvalSets ensures the eval sets table.
	SchemaEvalSets SchemaTarget = 1 << iota
	// SchemaEvalCases ensures the eval cases table.
	SchemaEvalCases
	// SchemaMetrics ensures the metrics table.
	SchemaMetrics
	// SchemaEvalSetResults ensures the eval set results table.
	SchemaEvalSetResults

	// SchemaAll ensures all evaluation tables.
	SchemaAll = SchemaEvalSets | SchemaEvalCases | SchemaMetrics | SchemaEvalSetResults
)

// BuildTables builds table names with the given prefix.
func BuildTables(prefix string) Tables {
	return Tables{
		EvalSets:       sqldb.BuildTableName(prefix, TableNameEvalSets),
		EvalCases:      sqldb.BuildTableName(prefix, TableNameEvalCases),
		Metrics:        sqldb.BuildTableName(prefix, TableNameMetrics),
		EvalSetResults: sqldb.BuildTableName(prefix, TableNameEvalSetResults),
	}
}

// EnsureSchema creates selected evaluation MySQL tables if they do not exist.
func EnsureSchema(ctx context.Context, db storage.Client, tables Tables, target SchemaTarget) error {
	if target == 0 {
		return errors.New("no schema target specified")
	}

	tableDefs := []tableDefinition{}
	indexDefs := []indexDefinition{}

	for _, spec := range schemaSpecs {
		if target&spec.target == 0 {
			continue
		}
		tableName := spec.tableName(tables)
		tableDefs = append(tableDefs, tableDefinition{
			name:     tableName,
			template: spec.tableSQL,
		})
		for _, idx := range spec.indexes {
			indexDefs = append(indexDefs, indexDefinition{
				table:    tableName,
				name:     idx.name,
				template: idx.template,
			})
		}
	}

	for _, tableDef := range tableDefs {
		query := strings.ReplaceAll(tableDef.template, "{{TABLE_NAME}}", tableDef.name)
		if _, err := db.Exec(ctx, query); err != nil {
			return fmt.Errorf("create table %s failed: %w", tableDef.name, err)
		}
	}

	for _, indexDef := range indexDefs {
		query := strings.ReplaceAll(indexDef.template, "{{TABLE_NAME}}", indexDef.table)
		query = strings.ReplaceAll(query, "{{INDEX_NAME}}", indexDef.name)
		if _, err := db.Exec(ctx, query); err != nil {
			if IsDuplicateKeyName(err) {
				continue
			}
			return fmt.Errorf("create index %s on table %s failed: %w", indexDef.name, indexDef.table, err)
		}
	}
	return nil
}

const (
	sqlCreateEvalSetsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT NOT NULL AUTO_INCREMENT,
			app_name VARCHAR(255) NOT NULL,
			eval_set_id VARCHAR(255) NOT NULL,
			name VARCHAR(255) NOT NULL,
			description TEXT DEFAULT NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	sqlCreateEvalSetsUniqueIndex = `
		CREATE UNIQUE INDEX {{INDEX_NAME}} ON {{TABLE_NAME}}(app_name, eval_set_id)`

	sqlCreateEvalSetsAppCreatedIndex = `
		CREATE INDEX {{INDEX_NAME}} ON {{TABLE_NAME}}(app_name, created_at)`

	sqlCreateEvalCasesTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT NOT NULL AUTO_INCREMENT,
			app_name VARCHAR(255) NOT NULL,
			eval_set_id VARCHAR(255) NOT NULL,
			eval_id VARCHAR(255) NOT NULL,
			eval_mode VARCHAR(32) NOT NULL DEFAULT '',
			eval_case JSON NOT NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	sqlCreateEvalCasesUniqueIndex = `
		CREATE UNIQUE INDEX {{INDEX_NAME}} ON {{TABLE_NAME}}(app_name, eval_set_id, eval_id)`

	sqlCreateEvalCasesOrderIndex = `
		CREATE INDEX {{INDEX_NAME}} ON {{TABLE_NAME}}(app_name, eval_set_id, id)`

	sqlCreateMetricsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT NOT NULL AUTO_INCREMENT,
			app_name VARCHAR(255) NOT NULL,
			eval_set_id VARCHAR(255) NOT NULL,
			metric_name VARCHAR(255) NOT NULL,
			metric JSON NOT NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	sqlCreateMetricsUniqueIndex = `
		CREATE UNIQUE INDEX {{INDEX_NAME}} ON {{TABLE_NAME}}(app_name, eval_set_id, metric_name)`

	sqlCreateMetricsAppSetIndex = `
		CREATE INDEX {{INDEX_NAME}} ON {{TABLE_NAME}}(app_name, eval_set_id)`

	sqlCreateEvalSetResultsTable = `
		CREATE TABLE IF NOT EXISTS {{TABLE_NAME}} (
			id BIGINT NOT NULL AUTO_INCREMENT,
			app_name VARCHAR(255) NOT NULL,
			eval_set_result_id VARCHAR(255) NOT NULL,
			eval_set_id VARCHAR(255) NOT NULL,
			eval_set_result_name VARCHAR(255) NOT NULL,
			eval_case_results JSON NOT NULL,
			summary JSON DEFAULT NULL,
			created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

	sqlCreateEvalSetResultsUniqueIndex = `
		CREATE UNIQUE INDEX {{INDEX_NAME}} ON {{TABLE_NAME}}(app_name, eval_set_result_id)`

	sqlCreateEvalSetResultsAppCreatedIndex = `
		CREATE INDEX {{INDEX_NAME}} ON {{TABLE_NAME}}(app_name, created_at)`

	sqlCreateEvalSetResultsAppSetCreatedIndex = `
		CREATE INDEX {{INDEX_NAME}} ON {{TABLE_NAME}}(app_name, eval_set_id, created_at)`
)
