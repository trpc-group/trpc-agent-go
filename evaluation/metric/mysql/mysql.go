//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/mysqldb"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

var _ metric.Manager = (*manager)(nil)

type manager struct {
	opts   options
	db     storage.Client
	tables mysqldb.Tables
}

// New creates a MySQL-backed metric manager.
func New(opts ...Option) (metric.Manager, error) {
	options := newOptions(opts...)
	db, err := mysqldb.BuildClient(options.dsn, options.instanceName, options.extraOptions)
	if err != nil {
		return nil, fmt.Errorf("create mysql client failed: %w", err)
	}
	tables := mysqldb.BuildTables(options.tablePrefix)
	m := &manager{
		opts:   *options,
		db:     db,
		tables: tables,
	}
	if !options.skipDBInit {
		ctx, cancel := context.WithTimeout(context.Background(), options.initTimeout)
		defer cancel()
		if err := mysqldb.EnsureSchema(ctx, db, tables, mysqldb.SchemaMetrics); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("init database failed: %w", err)
		}
	}
	return m, nil
}

// Close implements metric.Manager.
func (m *manager) Close() error {
	if m.db == nil {
		return nil
	}
	return m.db.Close()
}

// List lists metric names for the specified evaluation set from MySQL.
func (m *manager) List(ctx context.Context, appName, evalSetID string) ([]string, error) {
	if appName == "" {
		return nil, errors.New("empty app name")
	}
	if evalSetID == "" {
		return nil, errors.New("empty eval set id")
	}
	query := fmt.Sprintf(
		"SELECT metric_name FROM %s WHERE app_name = ? AND eval_set_id = ? ORDER BY metric_name ASC",
		m.tables.Metrics,
	)
	var names []string
	if err := m.db.Query(ctx, func(rows *sql.Rows) error {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		names = append(names, name)
		return nil
	}, query, appName, evalSetID); err != nil {
		return nil, fmt.Errorf("list metrics for app %s: %w", appName, err)
	}
	if names == nil {
		names = []string{}
	}
	return names, nil
}

// Get retrieves a metric definition from MySQL.
func (m *manager) Get(ctx context.Context, appName, evalSetID, metricName string) (*metric.EvalMetric, error) {
	if appName == "" {
		return nil, errors.New("empty app name")
	}
	if evalSetID == "" {
		return nil, errors.New("empty eval set id")
	}
	if metricName == "" {
		return nil, errors.New("empty metric name")
	}
	var payload []byte
	query := fmt.Sprintf(
		"SELECT metric FROM %s WHERE app_name = ? AND eval_set_id = ? AND metric_name = ?",
		m.tables.Metrics,
	)
	if err := m.db.QueryRow(ctx, []any{&payload}, query, appName, evalSetID, metricName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricName, os.ErrNotExist)
		}
		return nil, fmt.Errorf("get metric %s.%s.%s: %w", appName, evalSetID, metricName, err)
	}
	var res metric.EvalMetric
	if err := json.Unmarshal(payload, &res); err != nil {
		return nil, fmt.Errorf("unmarshal metric %s.%s.%s: %w", appName, evalSetID, metricName, err)
	}
	return &res, nil
}

// Add inserts a new metric definition into MySQL.
func (m *manager) Add(ctx context.Context, appName, evalSetID string, metricInput *metric.EvalMetric) error {
	if appName == "" {
		return errors.New("empty app name")
	}
	if evalSetID == "" {
		return errors.New("empty eval set id")
	}
	if metricInput == nil {
		return errors.New("metric is nil")
	}
	if metricInput.MetricName == "" {
		return errors.New("metric name is empty")
	}
	payload, err := json.Marshal(metricInput)
	if err != nil {
		return fmt.Errorf("marshal metric: %w", err)
	}
	query := fmt.Sprintf(
		"INSERT INTO %s (app_name, eval_set_id, metric_name, metric) VALUES (?, ?, ?, ?)",
		m.tables.Metrics,
	)
	if _, err := m.db.Exec(ctx, query, appName, evalSetID, metricInput.MetricName, payload); err != nil {
		if mysqldb.IsDuplicateEntry(err) {
			return fmt.Errorf("metric %s.%s.%s already exists", appName, evalSetID, metricInput.MetricName)
		}
		return fmt.Errorf("add metric %s.%s.%s: %w", appName, evalSetID, metricInput.MetricName, err)
	}
	return nil
}

// Delete removes a metric definition from MySQL.
func (m *manager) Delete(ctx context.Context, appName, evalSetID, metricName string) error {
	if appName == "" {
		return errors.New("empty app name")
	}
	if evalSetID == "" {
		return errors.New("empty eval set id")
	}
	if metricName == "" {
		return errors.New("metric name is empty")
	}
	query := fmt.Sprintf(
		"DELETE FROM %s WHERE app_name = ? AND eval_set_id = ? AND metric_name = ?",
		m.tables.Metrics,
	)
	res, err := m.db.Exec(ctx, query, appName, evalSetID, metricName)
	if err != nil {
		return fmt.Errorf("delete metric %s.%s.%s: %w", appName, evalSetID, metricName, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected failed: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricName, os.ErrNotExist)
	}
	return nil
}

// Update updates an existing metric definition in MySQL.
func (m *manager) Update(ctx context.Context, appName, evalSetID string, metricInput *metric.EvalMetric) error {
	if appName == "" {
		return errors.New("empty app name")
	}
	if evalSetID == "" {
		return errors.New("empty eval set id")
	}
	if metricInput == nil {
		return errors.New("metric is nil")
	}
	if metricInput.MetricName == "" {
		return errors.New("metric name is empty")
	}
	payload, err := json.Marshal(metricInput)
	if err != nil {
		return fmt.Errorf("marshal metric: %w", err)
	}
	query := fmt.Sprintf(
		"UPDATE %s SET metric = ?, updated_at = CURRENT_TIMESTAMP(6) WHERE app_name = ? AND eval_set_id = ? AND metric_name = ?",
		m.tables.Metrics,
	)
	res, err := m.db.Exec(ctx, query, payload, appName, evalSetID, metricInput.MetricName)
	if err != nil {
		return fmt.Errorf("update metric %s.%s.%s: %w", appName, evalSetID, metricInput.MetricName, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected failed: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricInput.MetricName, os.ErrNotExist)
	}
	return nil
}
