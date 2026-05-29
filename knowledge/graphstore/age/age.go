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
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graphstore"
	"trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

const (
	nodeLabel                          = "Node"
	maxRelationshipPatternCombinations = 256
)

var (
	_ graphstore.Store = (*Store)(nil)

	validIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Store is an Apache AGE graph backend.
type Store struct {
	client     postgres.Client
	option     options
	labelMu    sync.Mutex
	edgeLabels map[string]struct{}
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
		client:     client,
		option:     option,
		edgeLabels: make(map[string]struct{}),
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
	return s.withLockedAgeTx(ctx, func(tx *sql.Tx) error {
		var exists int
		row := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM ag_catalog.ag_graph WHERE name = $1`,
			s.option.graphName,
		)
		if err := row.Scan(&exists); err != nil {
			return fmt.Errorf("age: check graph: %w", err)
		}
		if exists == 0 {
			if _, err := tx.ExecContext(ctx, `SELECT ag_catalog.create_graph($1)`, s.option.graphName); err != nil {
				return fmt.Errorf("age: create graph: %w", err)
			}
		}
		return s.ensureVertexLabelTx(ctx, tx, nodeLabel)
	})
}

func (s *Store) ensureVertexLabelTx(ctx context.Context, tx *sql.Tx, label string) error {
	return s.ensureLabelTx(ctx, tx, label, "v", "create_vlabel")
}

func (s *Store) ensureEdgeLabels(ctx context.Context, labels []string) error {
	if len(labels) == 0 {
		return nil
	}
	s.labelMu.Lock()
	defer s.labelMu.Unlock()

	var missing []string
	for _, label := range labels {
		if _, ok := s.edgeLabels[label]; !ok {
			missing = append(missing, label)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := s.withLockedAgeTx(ctx, func(tx *sql.Tx) error {
		for _, label := range missing {
			if err := s.ensureLabelTx(ctx, tx, label, "e", "create_elabel"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, label := range missing {
		s.edgeLabels[label] = struct{}{}
	}
	return nil
}

func (s *Store) ensureLabelTx(ctx context.Context, tx *sql.Tx, label, kind, createFunc string) error {
	var exists int
	row := tx.QueryRowContext(ctx, `
		SELECT COUNT(1)
		FROM ag_catalog.ag_label label
		JOIN ag_catalog.ag_graph graph ON label.graph = graph.graphid
		WHERE graph.name = $1 AND label.name = $2 AND label.kind = $3
	`, s.option.graphName, label, kind)
	if err := row.Scan(&exists); err != nil {
		return fmt.Errorf("age: check label %s: %w", label, err)
	}
	if exists > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`SELECT ag_catalog.%s($1, $2)`, createFunc), s.option.graphName, label); err != nil {
		return fmt.Errorf("age: create label %s: %w", label, err)
	}
	return nil
}

func edgeTypes(edges []*graph.Edge) ([]string, error) {
	seen := make(map[string]struct{})
	for i, edge := range edges {
		if edge == nil {
			return nil, fmt.Errorf("age: edge at index %d is nil", i)
		}
		if edge.FromID == "" || edge.ToID == "" {
			return nil, fmt.Errorf("age: edge at index %d has empty endpoint", i)
		}
		if err := validateIdentifier("edge type", edge.Type); err != nil {
			return nil, err
		}
		seen[edge.Type] = struct{}{}
	}
	labels := make([]string, 0, len(seen))
	for label := range seen {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels, nil
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
	edgeLabels, err := edgeTypes(edges)
	if err != nil {
		return err
	}
	if err := s.ensureEdgeLabels(ctx, edgeLabels); err != nil {
		return err
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
	queryTruncated := false
	if err := s.withAgeTx(ctx, func(tx *sql.Tx) error {
		startNodes, err := s.queryNodesByIDs(ctx, tx, query.StartIDs)
		if err != nil {
			return err
		}
		nodes = append(nodes, startNodes...)

		patterns, err := relationshipPatterns(query.Direction, query.EdgeTypes, depth)
		if err != nil {
			return err
		}
		for _, startID := range query.StartIDs {
			for _, pattern := range patterns {
				queryLimit := maxNodes + 1
				cypher := traverseNodeQueryCypher(startID, pattern, queryLimit)
				resultNodes, err := s.queryNodes(ctx, tx, cypher)
				if err != nil {
					return err
				}
				if len(resultNodes) > maxNodes {
					queryTruncated = true
					resultNodes = resultNodes[:maxNodes]
				}
				nodes = append(nodes, resultNodes...)

				// MaxNodes and Truncated describe node result completeness.
				// Edges are bounded by the same value only to keep topology compact.
				edgeCypher := traverseEdgeQueryCypher(startID, pattern, maxNodes)
				resultEdges, err := s.queryEdges(ctx, tx, edgeCypher)
				if err != nil {
					return err
				}
				edges = append(edges, resultEdges...)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	resultNodes := uniqueNodes(nodes)
	limitedNodes := limitNodes(resultNodes, maxNodes)
	truncated := queryTruncated || len(resultNodes) > len(limitedNodes)
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
	truncated := false
	if err := s.withAgeTx(ctx, func(tx *sql.Tx) error {
		patterns, err := relationshipPatterns(query.Direction, query.EdgeTypes, depth)
		if err != nil {
			return err
		}
		for i, pattern := range patterns {
			// A previous pattern exactly filled the limit without overflow;
			// probe remaining patterns (including current) to set truncated.
			if len(paths) >= maxPaths {
				return s.probeMorePaths(ctx, tx, query.FromID, query.ToID, patterns[i:], &truncated)
			}
			remaining := maxPaths - len(paths)
			cypher := pathQueryCypher(query.FromID, query.ToID, pattern, remaining+1)
			rawPaths, err := s.queryAgPaths(ctx, tx, cypher)
			if err != nil {
				return err
			}
			if len(rawPaths) > remaining {
				truncated = true
				rawPaths = rawPaths[:remaining]
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
			// Current pattern filled the limit; probe later patterns if
			// truncation was not already detected by overflow above.
			if len(paths) >= maxPaths {
				if !truncated && i+1 < len(patterns) {
					return s.probeMorePaths(ctx, tx, query.FromID, query.ToID, patterns[i+1:], &truncated)
				}
				return nil
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return &graph.PathResult{Paths: paths, Truncated: truncated}, nil
}

func (s *Store) hasAnyPath(ctx context.Context, tx *sql.Tx, fromID, toID string, patterns []string) (bool, error) {
	for _, pattern := range patterns {
		rawPaths, err := s.queryAgPaths(ctx, tx, pathQueryCypher(fromID, toID, pattern, 1))
		if err != nil {
			return false, err
		}
		if len(rawPaths) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) probeMorePaths(ctx context.Context, tx *sql.Tx, fromID, toID string, patterns []string, truncated *bool) error {
	hasMore, err := s.hasAnyPath(ctx, tx, fromID, toID, patterns)
	if err != nil {
		return err
	}
	if hasMore {
		*truncated = true
	}
	return nil
}

func pathQueryCypher(fromID, toID, pattern string, limit int) string {
	return fmt.Sprintf(
		`MATCH p=(from:%s {id: %s})%s(to:%s {id: %s}) WITH p ORDER BY length(p) ASC LIMIT %d RETURN [node IN nodes(p) | properties(node).id], [edge IN relationships(p) | properties(edge).id], [edge IN relationships(p) | properties(startNode(edge)).id], [edge IN relationships(p) | properties(endNode(edge)).id], [edge IN relationships(p) | type(edge)]`,
		nodeLabel,
		cypherString(fromID),
		pattern,
		nodeLabel,
		cypherString(toID),
		limit,
	)
}

func traverseNodeQueryCypher(startID, pattern string, limit int) string {
	return fmt.Sprintf(
		`MATCH p=(start:%s {id: %s})%s(n:%s) UNWIND nodes(p) AS node WITH node, min(length(p)) AS distance ORDER BY distance ASC, node.id ASC LIMIT %d RETURN node.id, node.name, node.content, node.metadata`,
		nodeLabel,
		cypherString(startID),
		pattern,
		nodeLabel,
		limit,
	)
}

func traverseEdgeQueryCypher(startID, pattern string, maxNodes int) string {
	return fmt.Sprintf(
		`MATCH p=(start:%s {id: %s})%s(n:%s) UNWIND relationships(p) AS edge RETURN DISTINCT properties(edge).id, properties(startNode(edge)).id, properties(endNode(edge)).id, type(edge), properties(edge).metadata LIMIT %d`,
		nodeLabel,
		cypherString(startID),
		pattern,
		nodeLabel,
		maxNodes,
	)
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

func (s *Store) withLockedAgeTx(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.client.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "trpc-agent-go-age:"+s.option.graphName); err != nil {
			return fmt.Errorf("age: acquire advisory lock: %w", err)
		}
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

func relationshipPatterns(direction graph.Direction, edgeTypes []string, depth int) ([]string, error) {
	if depth <= 0 {
		depth = 1
	}
	if direction != "" && direction != graph.DirectionOut && direction != graph.DirectionIn && direction != graph.DirectionBoth {
		return nil, fmt.Errorf("age: unsupported direction %q", direction)
	}
	normalized, err := normalizeEdgeTypes(edgeTypes)
	if err != nil {
		return nil, err
	}
	if len(normalized) <= 1 {
		pattern, err := relationshipPattern(direction, normalized, depth)
		if err != nil {
			return nil, err
		}
		return []string{pattern}, nil
	}

	var patterns []string
	for pathLen := 1; pathLen <= depth; pathLen++ {
		patternsForLen := powInt(len(normalized), pathLen)
		if len(patterns)+patternsForLen > maxRelationshipPatternCombinations {
			return nil, fmt.Errorf("age: too many edge type combinations: %d edge type(s) with max_depth %d exceeds %d", len(normalized), depth, maxRelationshipPatternCombinations)
		}
		sequences := make([][]string, 0, patternsForLen)
		buildEdgeTypeSequences(&sequences, nil, normalized, pathLen)
		for _, sequence := range sequences {
			patterns = append(patterns, relationshipPatternForSequence(direction, sequence))
		}
	}
	return patterns, nil
}

func buildEdgeTypeSequences(result *[][]string, prefix []string, edgeTypes []string, remaining int) {
	if remaining == 0 {
		sequence := make([]string, len(prefix))
		copy(sequence, prefix)
		*result = append(*result, sequence)
		return
	}
	for _, edgeType := range edgeTypes {
		buildEdgeTypeSequences(result, append(prefix, edgeType), edgeTypes, remaining-1)
	}
}

func relationshipPatternForSequence(direction graph.Direction, edgeTypes []string) string {
	var b strings.Builder
	for i, edgeType := range edgeTypes {
		if i > 0 {
			b.WriteString(fmt.Sprintf(`(:%s)`, nodeLabel))
		}
		b.WriteString(relationshipSegment(direction, edgeType))
	}
	return b.String()
}

func relationshipSegment(direction graph.Direction, edgeType string) string {
	switch direction {
	case "", graph.DirectionOut:
		return fmt.Sprintf(`-[:%s]->`, edgeType)
	case graph.DirectionIn:
		return fmt.Sprintf(`<-[:%s]-`, edgeType)
	case graph.DirectionBoth:
		return fmt.Sprintf(`-[:%s]-`, edgeType)
	default:
		return ""
	}
}

func edgeTypeFilter(edgeTypes []string) (string, error) {
	normalized, err := normalizeEdgeTypes(edgeTypes)
	if err != nil {
		return "", err
	}
	if len(normalized) == 0 {
		return "", nil
	}
	return ":" + strings.Join(normalized, "|"), nil
}

func normalizeEdgeTypes(edgeTypes []string) ([]string, error) {
	seen := make(map[string]struct{}, len(edgeTypes))
	normalized := make([]string, 0, len(edgeTypes))
	for _, edgeType := range edgeTypes {
		if err := validateIdentifier("edge type", edgeType); err != nil {
			return nil, err
		}
		if _, ok := seen[edgeType]; ok {
			continue
		}
		seen[edgeType] = struct{}{}
		normalized = append(normalized, edgeType)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func powInt(base, exp int) int {
	result := 1
	for i := 0; i < exp; i++ {
		result *= base
	}
	return result
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
	// AGE returns string agtype values in JSON-quoted form (e.g. "\"hello\\nworld\"").
	// Use json.Unquote via json.Unmarshal to correctly handle escape sequences.
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		var unquoted string
		if err := json.Unmarshal([]byte(value), &unquoted); err == nil {
			return unquoted
		}
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
	if value == "" {
		return nil
	}
	var values []*string
	if err := json.Unmarshal([]byte(value), &values); err == nil {
		result := make([]string, 0, len(values))
		for _, value := range values {
			if value == nil {
				result = append(result, "")
				continue
			}
			result = append(result, *value)
		}
		return result
	}
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
