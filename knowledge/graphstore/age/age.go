//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package age provides an Apache AGE backed graph store.
package age

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graphstore"
	"trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

const (
	nodeLabel = "Node"
)

var (
	_ graphstore.Store = (*Store)(nil)

	validIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Store is an Apache AGE graph backend.
type Store struct {
	client postgres.Client
	option options
}

// New creates an Apache AGE graph store.
func New(opts ...Option) (*Store, error) {
	option := defaultOptions
	for _, opt := range opts {
		opt(&option)
	}
	if err := validateIdentifier("graph name", option.graphName); err != nil {
		return nil, err
	}

	builderOpts, err := option.builderOptions()
	if err != nil {
		return nil, err
	}
	client, err := postgres.GetClientBuilder()(context.Background(), builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("age: create postgres client: %w", err)
	}

	store := &Store{
		client: client,
		option: option,
	}
	if err := store.initDB(context.Background()); err != nil {
		_ = client.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) initDB(ctx context.Context) error {
	if _, err := s.client.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS age"); err != nil {
		return fmt.Errorf("age: create extension: %w", err)
	}
	return s.withAgeTx(ctx, func(tx *sql.Tx) error {
		var exists int
		row := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM ag_catalog.ag_graph WHERE name = $1`,
			s.option.graphName,
		)
		if err := row.Scan(&exists); err != nil {
			return fmt.Errorf("age: check graph: %w", err)
		}
		if exists > 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `SELECT ag_catalog.create_graph($1)`, s.option.graphName); err != nil {
			return fmt.Errorf("age: create graph: %w", err)
		}
		return nil
	})
}

// AddNodes inserts or updates graph nodes in AGE.
func (s *Store) AddNodes(
	ctx context.Context,
	nodes []*graph.Node,
) error {
	if len(nodes) == 0 {
		return nil
	}
	return s.withAgeTx(ctx, func(tx *sql.Tx) error {
		for i, node := range nodes {
			if node == nil {
				return fmt.Errorf("age: node at index %d is nil", i)
			}
			if node.ID == "" {
				return fmt.Errorf("age: node at index %d has empty id", i)
			}
			cypher := fmt.Sprintf(
				`MERGE (n:%s {id: %s}) SET n.name = %s, n.content = %s, n.metadata = %s`,
				nodeLabel,
				cypherString(node.ID),
				cypherString(node.Name),
				cypherString(node.Content),
				cypherValue(node.Metadata),
			)
			if err := s.execCypher(ctx, tx, cypher); err != nil {
				return err
			}
		}
		return nil
	})
}

// AddEdges inserts or updates graph edges in AGE.
func (s *Store) AddEdges(
	ctx context.Context,
	edges []*graph.Edge,
) error {
	if len(edges) == 0 {
		return nil
	}
	return s.withAgeTx(ctx, func(tx *sql.Tx) error {
		for i, edge := range edges {
			if edge == nil {
				return fmt.Errorf("age: edge at index %d is nil", i)
			}
			if edge.FromID == "" || edge.ToID == "" {
				return fmt.Errorf("age: edge at index %d has empty endpoint", i)
			}
			if err := validateIdentifier("edge type", edge.Type); err != nil {
				return err
			}
			setParts := []string{fmt.Sprintf("e.metadata = %s", cypherValue(edge.Metadata))}
			if edge.ID != "" {
				setParts = append([]string{fmt.Sprintf("e.id = %s", cypherString(edge.ID))}, setParts...)
			}
			cypher := fmt.Sprintf(
				`MATCH (from:%s {id: %s}), (to:%s {id: %s}) MERGE (from)-[e:%s]->(to) SET %s`,
				nodeLabel,
				cypherString(edge.FromID),
				nodeLabel,
				cypherString(edge.ToID),
				edge.Type,
				strings.Join(setParts, ", "),
			)
			if err := s.execCypher(ctx, tx, cypher); err != nil {
				return err
			}
		}
		return nil
	})
}

// Traverse runs a graph traversal from one or more start nodes.
func (s *Store) Traverse(ctx context.Context, query *graph.TraverseQuery) (*graph.TraverseResult, error) {
	if query == nil {
		return nil, errors.New("age: traverse query is required")
	}
	if len(query.StartIDs) == 0 {
		return nil, errors.New("age: start_ids cannot be empty")
	}
	depth := query.MaxDepth
	if depth <= 0 {
		depth = 1
	}
	maxNodes := query.MaxNodes
	if maxNodes <= 0 {
		maxNodes = 100
	}

	nodes := make([]*graph.Node, 0)
	edges := make([]*graph.Edge, 0)
	if err := s.withAgeTx(ctx, func(tx *sql.Tx) error {
		startNodes, err := s.queryNodesByIDs(ctx, tx, query.StartIDs)
		if err != nil {
			return err
		}
		nodes = append(nodes, startNodes...)

		pattern, err := relationshipPattern(query.Direction, query.EdgeTypes, depth)
		if err != nil {
			return err
		}
		for _, startID := range query.StartIDs {
			cypher := fmt.Sprintf(
				`MATCH p=(start:%s {id: %s})%s(n:%s) UNWIND nodes(p) AS node RETURN DISTINCT node.id, node.name, node.content, node.metadata LIMIT %d`,
				nodeLabel,
				cypherString(startID),
				pattern,
				nodeLabel,
				maxNodes,
			)
			resultNodes, err := s.queryNodes(ctx, tx, cypher)
			if err != nil {
				return err
			}
			nodes = append(nodes, resultNodes...)

			edgeCypher := fmt.Sprintf(
				`MATCH p=(start:%s {id: %s})%s(n:%s) UNWIND relationships(p) AS edge RETURN DISTINCT edge.id, startNode(edge).id, endNode(edge).id, type(edge), edge.metadata LIMIT %d`,
				nodeLabel,
				cypherString(startID),
				pattern,
				nodeLabel,
				maxNodes,
			)
			resultEdges, err := s.queryEdges(ctx, tx, edgeCypher)
			if err != nil {
				return err
			}
			edges = append(edges, resultEdges...)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	resultNodes := uniqueNodes(nodes)
	limitedNodes := limitNodes(resultNodes, maxNodes)
	truncated := len(resultNodes) > len(limitedNodes)
	return &graph.TraverseResult{
		Nodes:     limitedNodes,
		Edges:     filterEdgesByNodes(uniqueEdges(edges), limitedNodes),
		Truncated: truncated,
	}, nil
}

// FindPaths finds paths between two graph nodes.
func (s *Store) FindPaths(ctx context.Context, query *graph.PathQuery) (*graph.PathResult, error) {
	if query == nil {
		return nil, errors.New("age: path query is required")
	}
	if query.FromID == "" || query.ToID == "" {
		return nil, errors.New("age: from_id and to_id are required")
	}
	depth := query.MaxDepth
	if depth <= 0 {
		depth = 5
	}
	maxPaths := query.MaxPaths
	if maxPaths <= 0 {
		maxPaths = 10
	}

	var paths []*graph.Path
	if err := s.withAgeTx(ctx, func(tx *sql.Tx) error {
		pattern, err := relationshipPattern(query.Direction, query.EdgeTypes, depth)
		if err != nil {
			return err
		}
		cypher := fmt.Sprintf(
			`MATCH p=(from:%s {id: %s})%s(to:%s {id: %s}) RETURN [node IN nodes(p) | node.id], [edge IN relationships(p) | edge.id], [edge IN relationships(p) | startNode(edge).id], [edge IN relationships(p) | endNode(edge).id], [edge IN relationships(p) | type(edge)] LIMIT %d`,
			nodeLabel,
			cypherString(query.FromID),
			pattern,
			nodeLabel,
			cypherString(query.ToID),
			maxPaths,
		)
		rawPaths, err := s.queryAgPaths(ctx, tx, cypher)
		if err != nil {
			return err
		}
		for _, path := range rawPaths {
			nodes, err := s.queryNodesByIDs(ctx, tx, path.nodeIDs)
			if err != nil {
				return err
			}
			paths = append(paths, &graph.Path{
				Nodes: nodes,
				Edges: pathEdges(path),
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return &graph.PathResult{Paths: paths}, nil
}

// Close closes the graph client.
func (s *Store) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

func (s *Store) withAgeTx(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.client.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `LOAD 'age'`); err != nil {
			return fmt.Errorf("age: load extension: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SET search_path = ag_catalog, "$user", public`); err != nil {
			return fmt.Errorf("age: set search path: %w", err)
		}
		return fn(tx)
	})
}

func (s *Store) execCypher(ctx context.Context, tx *sql.Tx, cypher string) error {
	if _, err := tx.ExecContext(
		ctx,
		s.cypherSQL(cypher, `result agtype`),
	); err != nil {
		return fmt.Errorf("age: execute cypher: %w", err)
	}
	return nil
}

func (s *Store) cypherSQL(cypher, columns string) string {
	return fmt.Sprintf(`SELECT * FROM cypher('%s', %s) AS (%s)`, s.option.graphName, dollarQuote(cypher), columns)
}

type agPath struct {
	nodeIDs   []string
	edgeIDs   []string
	fromIDs   []string
	toIDs     []string
	edgeTypes []string
}

func (s *Store) queryAgPaths(ctx context.Context, tx *sql.Tx, cypher string) ([]*agPath, error) {
	rows, err := tx.QueryContext(
		ctx,
		s.cypherSQL(cypher, `node_ids agtype, edge_ids agtype, from_ids agtype, to_ids agtype, edge_types agtype`),
	)
	if err != nil {
		return nil, fmt.Errorf("age: query path cypher: %w", err)
	}
	defer rows.Close()

	var paths []*agPath
	for rows.Next() {
		var rawNodeIDs, rawEdgeIDs, rawFromIDs, rawToIDs, rawEdgeTypes string
		if err := rows.Scan(&rawNodeIDs, &rawEdgeIDs, &rawFromIDs, &rawToIDs, &rawEdgeTypes); err != nil {
			return nil, err
		}
		paths = append(paths, &agPath{
			nodeIDs:   parseAgStringList(rawNodeIDs),
			edgeIDs:   parseAgStringList(rawEdgeIDs),
			fromIDs:   parseAgStringList(rawFromIDs),
			toIDs:     parseAgStringList(rawToIDs),
			edgeTypes: parseAgStringList(rawEdgeTypes),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

func pathEdges(path *agPath) []*graph.Edge {
	if path == nil {
		return nil
	}
	n := len(path.edgeTypes)
	if len(path.fromIDs) < n {
		n = len(path.fromIDs)
	}
	if len(path.toIDs) < n {
		n = len(path.toIDs)
	}
	edges := make([]*graph.Edge, 0, n)
	for i := 0; i < n; i++ {
		edge := &graph.Edge{
			FromID: path.fromIDs[i],
			ToID:   path.toIDs[i],
			Type:   path.edgeTypes[i],
		}
		if i < len(path.edgeIDs) {
			edge.ID = path.edgeIDs[i]
		}
		if edge.ID == "" {
			edge.ID = edge.FromID + ":" + edge.Type + ":" + edge.ToID
		}
		edges = append(edges, edge)
	}
	return edges
}

func filterEdgesByNodes(edges []*graph.Edge, nodes []*graph.Node) []*graph.Edge {
	if len(edges) == 0 || len(nodes) == 0 {
		return nil
	}
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node == nil || node.ID == "" {
			continue
		}
		nodeIDs[node.ID] = struct{}{}
	}
	filtered := make([]*graph.Edge, 0, len(edges))
	for _, edge := range edges {
		if edge == nil {
			continue
		}
		if _, ok := nodeIDs[edge.FromID]; !ok {
			continue
		}
		if _, ok := nodeIDs[edge.ToID]; !ok {
			continue
		}
		filtered = append(filtered, edge)
	}
	return filtered
}

func (s *Store) queryNodesByIDs(ctx context.Context, tx *sql.Tx, ids []string) ([]*graph.Node, error) {
	nodes := make([]*graph.Node, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		cypher := fmt.Sprintf(
			`MATCH (node:%s {id: %s}) RETURN node.id, node.name, node.content, node.metadata LIMIT 1`,
			nodeLabel,
			cypherString(id),
		)
		resultNodes, err := s.queryNodes(ctx, tx, cypher)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, resultNodes...)
	}
	return nodes, nil
}

func (s *Store) queryNodes(ctx context.Context, tx *sql.Tx, cypher string) ([]*graph.Node, error) {
	rows, err := tx.QueryContext(
		ctx,
		s.cypherSQL(cypher, `id agtype, name agtype, content agtype, metadata agtype`),
	)
	if err != nil {
		return nil, fmt.Errorf("age: query node cypher: %w", err)
	}
	defer rows.Close()

	var nodes []*graph.Node
	for rows.Next() {
		var rawID, rawName, rawContent, rawMetadata string
		if err := rows.Scan(&rawID, &rawName, &rawContent, &rawMetadata); err != nil {
			return nil, err
		}
		nodes = append(nodes, &graph.Node{
			ID:       parseAgString(rawID),
			Name:     parseAgString(rawName),
			Content:  parseAgString(rawContent),
			Metadata: parseAgMetadata(rawMetadata),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (s *Store) queryEdges(ctx context.Context, tx *sql.Tx, cypher string) ([]*graph.Edge, error) {
	rows, err := tx.QueryContext(
		ctx,
		s.cypherSQL(cypher, `id agtype, from_id agtype, to_id agtype, edge_type agtype, metadata agtype`),
	)
	if err != nil {
		return nil, fmt.Errorf("age: query edge cypher: %w", err)
	}
	defer rows.Close()

	var edges []*graph.Edge
	for rows.Next() {
		var rawID, rawFromID, rawToID, rawType, rawMetadata string
		if err := rows.Scan(&rawID, &rawFromID, &rawToID, &rawType, &rawMetadata); err != nil {
			return nil, err
		}
		edge := &graph.Edge{
			ID:       parseAgString(rawID),
			FromID:   parseAgString(rawFromID),
			ToID:     parseAgString(rawToID),
			Type:     parseAgString(rawType),
			Metadata: parseAgMetadata(rawMetadata),
		}
		if edge.ID == "" {
			edge.ID = edge.FromID + ":" + edge.Type + ":" + edge.ToID
		}
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return edges, nil
}

func relationshipPattern(direction graph.Direction, edgeTypes []string, depth int) (string, error) {
	typeFilter, err := edgeTypeFilter(edgeTypes)
	if err != nil {
		return "", err
	}
	switch direction {
	case "", graph.DirectionOut:
		return fmt.Sprintf(`-[%s*1..%d]->`, typeFilter, depth), nil
	case graph.DirectionIn:
		return fmt.Sprintf(`<-[%s*1..%d]-`, typeFilter, depth), nil
	case graph.DirectionBoth:
		return fmt.Sprintf(`-[%s*1..%d]-`, typeFilter, depth), nil
	default:
		return "", fmt.Errorf("age: unsupported direction %q", direction)
	}
}

func edgeTypeFilter(edgeTypes []string) (string, error) {
	if len(edgeTypes) == 0 {
		return "", nil
	}
	for _, edgeType := range edgeTypes {
		if err := validateIdentifier("edge type", edgeType); err != nil {
			return "", err
		}
	}
	return ":" + strings.Join(edgeTypes, "|"), nil
}

func validateIdentifier(name, value string) error {
	if value == "" {
		return fmt.Errorf("age: %s is required", name)
	}
	if !validIdentifier.MatchString(value) {
		return fmt.Errorf("age: invalid %s %q", name, value)
	}
	return nil
}

func cypherString(value string) string {
	b, _ := json.Marshal(value)
	return string(b)
}

func cypherValue(value any) string {
	if value == nil {
		return "null"
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return "null"
		}
		keys := make([]string, 0, rv.Len())
		for _, key := range rv.MapKeys() {
			keys = append(keys, key.String())
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s: %s", cypherKey(key), cypherReflectValue(rv.MapIndex(reflect.ValueOf(key)))))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case reflect.Slice, reflect.Array:
		parts := make([]string, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			parts = append(parts, cypherReflectValue(rv.Index(i)))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	b, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(b)
}

func cypherReflectValue(value reflect.Value) string {
	if !value.IsValid() {
		return "null"
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return "null"
		}
		return cypherReflectValue(value.Elem())
	}
	return cypherValue(value.Interface())
}

func cypherKey(key string) string {
	if validIdentifier.MatchString(key) {
		return key
	}
	return "`" + strings.ReplaceAll(key, "`", "``") + "`"
}

func dollarQuote(value string) string {
	for i := 0; ; i++ {
		tag := "age"
		if i > 0 {
			tag = fmt.Sprintf("age%d", i)
		}
		delimiter := "$" + tag + "$"
		if !strings.Contains(value, delimiter) {
			return delimiter + value + delimiter
		}
	}
}

func parseAgString(raw string) string {
	value := strings.TrimSpace(strings.TrimSuffix(raw, "::agtype"))
	if value == "null" {
		return ""
	}
	value = strings.Trim(value, `"`)
	return value
}

func parseAgMetadata(raw string) map[string]any {
	value := strings.TrimSpace(strings.TrimSuffix(raw, "::agtype"))
	if value == "" || value == "null" {
		return nil
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(value), &metadata); err != nil {
		return nil
	}
	return metadata
}

func parseAgStringList(raw string) []string {
	value := strings.TrimSpace(strings.TrimSuffix(raw, "::agtype"))
	value = strings.Trim(value, "[]")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		result = append(result, parseAgString(part))
	}
	return result
}

func uniqueNodes(nodes []*graph.Node) []*graph.Node {
	seen := make(map[string]bool, len(nodes))
	result := make([]*graph.Node, 0, len(nodes))
	for _, node := range nodes {
		if node == nil || node.ID == "" || seen[node.ID] {
			continue
		}
		seen[node.ID] = true
		result = append(result, node)
	}
	return result
}

func limitNodes(nodes []*graph.Node, limit int) []*graph.Node {
	if limit <= 0 || len(nodes) <= limit {
		return nodes
	}
	return nodes[:limit]
}

func uniqueEdges(edges []*graph.Edge) []*graph.Edge {
	seen := make(map[string]bool, len(edges))
	result := make([]*graph.Edge, 0, len(edges))
	for _, edge := range edges {
		if edge == nil {
			continue
		}
		key := edge.ID
		if key == "" {
			key = edge.FromID + ":" + edge.Type + ":" + edge.ToID
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, edge)
	}
	return result
}
