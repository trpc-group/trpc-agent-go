//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main starts a small Apache AGE graph viewer for the GraphRAG example.
package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
)

//go:embed index.html
var content embed.FS

var (
	addr      = flag.String("addr", util.GetEnvOrDefault("GRAPH_VIEWER_ADDR", "127.0.0.1:3012"), "HTTP listen address")
	graphName = flag.String("graph", util.GetEnvOrDefault("AGE_GRAPH_NAME", "knowledge_graph"), "Apache AGE graph name")
	nodeLimit = flag.Int("node-limit", 100, "Default node limit")
	edgeLimit = flag.Int("edge-limit", 1000, "Default edge limit")
	seedLimit = flag.Int("seed-limit", 12, "Search seed node limit")
)

const ageSessionSQL = `LOAD 'age'; SET search_path = ag_catalog, "$user", public`

type server struct {
	db        *sql.DB
	graphName string
}

type graphResponse struct {
	Nodes []node `json:"nodes"`
	Edges []edge `json:"edges"`
}

type node struct {
	ID         string         `json:"id"`
	Label      string         `json:"label"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind,omitempty"`
	Content    string         `json:"content"`
	Properties map[string]any `json:"properties"`
}

type edge struct {
	ID         string         `json:"id"`
	From       string         `json:"from"`
	To         string         `json:"to"`
	Label      string         `json:"label"`
	Properties map[string]any `json:"properties,omitempty"`
}

type vertexValue struct {
	ID         json.Number    `json:"id"`
	Label      string         `json:"label"`
	Properties map[string]any `json:"properties"`
}

type edgeValue struct {
	ID         json.Number    `json:"id"`
	Label      string         `json:"label"`
	StartID    json.Number    `json:"start_id"`
	EndID      json.Number    `json:"end_id"`
	Properties map[string]any `json:"properties"`
}

type edgeSummary struct {
	FromLabel string `json:"from_label"`
	EdgeLabel string `json:"edge_label"`
	ToLabel   string `json:"to_label"`
	Count     string `json:"count"`
}

func main() {
	flag.Parse()

	db, err := openDB(context.Background(), ageDSN())
	if err != nil {
		log.Fatalf("open AGE database: %v", err)
	}
	defer db.Close()

	s := &server{
		db:        db,
		graphName: *graphName,
	}
	mux := http.NewServeMux()
	static, err := fs.Sub(content, ".")
	if err != nil {
		log.Fatalf("prepare static files: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("/api/graph", s.handleGraph)
	mux.HandleFunc("/api/summary", s.handleSummary)
	mux.HandleFunc("/api/health", s.handleHealth)

	log.Printf("AGE graph viewer listening on http://%s", *addr)
	log.Printf("Graph: %s", *graphName)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func ageDSN() string {
	dsn := strings.TrimSpace(util.GetEnvOrDefault("AGE_DSN", ""))
	if dsn != "" {
		return dsn
	}
	host := util.GetEnvOrDefault("AGE_HOST", "127.0.0.1")
	port := util.GetEnvOrDefault("AGE_PORT", "5432")
	user := util.GetEnvOrDefault("AGE_USER", "root")
	password := util.GetEnvOrDefault("AGE_PASSWORD", "123")
	database := util.GetEnvOrDefault("AGE_DATABASE", "contextengine")
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, password),
		Host:     net.JoinHostPort(host, port),
		Path:     database,
		RawQuery: "sslmode=disable",
	}).String()
}

func openDB(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, ageSessionSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (s *server) ageConn(ctx context.Context) (*sql.Conn, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, ageSessionSQL); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "ok", "graph": s.graphName})
}

func (s *server) handleGraph(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	maxNodes := queryInt(r, "node_limit", *nodeLimit, 5000)
	maxEdges := queryInt(r, "edge_limit", *edgeLimit, 20000)
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		maxEdges = queryInt(r, "limit", maxEdges, 20000)
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))

	result, err := s.loadGraph(ctx, query, maxNodes, maxEdges)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, result)
}

func (s *server) loadGraph(ctx context.Context, query string, maxNodes, maxEdges int) (*graphResponse, error) {
	result := &graphResponse{}
	seenNodes := make(map[string]struct{})
	seenEdges := make(map[string]struct{})
	if query == "" {
		return s.loadDefaultGraph(ctx, result, seenNodes, seenEdges, maxNodes, maxEdges)
	}

	seeds, err := s.searchSeeds(ctx, query)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		addNode(result, seenNodes, seed, maxNodes)
		if id, ok := stringProperty(seed.Properties, "id"); ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	cypher := fmt.Sprintf(
		`MATCH (n:Node)-[r]-(m:Node) WHERE n.id IN [%s] RETURN n, r, m LIMIT %d`,
		cypherStringList(ids),
		maxEdges,
	)
	if err := s.queryTriples(ctx, cypher, result, seenNodes, seenEdges, maxNodes, maxEdges); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *server) loadDefaultGraph(ctx context.Context, result *graphResponse, seenNodes map[string]struct{}, seenEdges map[string]struct{}, maxNodes, maxEdges int) (*graphResponse, error) {
	cypher := fmt.Sprintf(`MATCH (n:Node)-[r]->(m:Node) RETURN n, r, m LIMIT %d`, maxEdges)
	if err := s.queryTriples(ctx, cypher, result, seenNodes, seenEdges, maxNodes, maxEdges); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *server) searchSeeds(ctx context.Context, query string) ([]vertexValue, error) {
	needle := cypherString(strings.ToLower(query))
	cypher := fmt.Sprintf(
		`MATCH (n:Node) WHERE toLower(n.name) CONTAINS %s OR toLower(n.id) CONTAINS %s OR toLower(n.content) CONTAINS %s RETURN n LIMIT %d`,
		needle,
		needle,
		needle,
		*seedLimit,
	)
	conn, err := s.ageConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, s.cypherSQL(cypher, "n agtype"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var seeds []vertexValue
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		v, err := parseVertex(raw)
		if err != nil {
			return nil, err
		}
		seeds = append(seeds, v)
	}
	return seeds, rows.Err()
}

func (s *server) queryTriples(ctx context.Context, cypher string, result *graphResponse, seenNodes map[string]struct{}, seenEdges map[string]struct{}, maxNodes, maxEdges int) error {
	conn, err := s.ageConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, s.cypherSQL(cypher, "n agtype, r agtype, m agtype"))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var rawN, rawR, rawM string
		if err := rows.Scan(&rawN, &rawR, &rawM); err != nil {
			return err
		}
		n, err := parseVertex(rawN)
		if err != nil {
			return err
		}
		r, err := parseEdge(rawR)
		if err != nil {
			return err
		}
		m, err := parseVertex(rawM)
		if err != nil {
			return err
		}
		if len(result.Edges) >= maxEdges {
			return nil
		}
		if !canAddTriple(seenNodes, n, m, maxNodes) {
			continue
		}
		addNode(result, seenNodes, n, maxNodes)
		addNode(result, seenNodes, m, maxNodes)
		addEdge(result, seenEdges, r)
	}
	return rows.Err()
}

func (s *server) handleSummary(w http.ResponseWriter, r *http.Request) {
	cypher := `MATCH (a)-[r]->(b) RETURN label(a), label(r), label(b), count(r) ORDER BY count(r) DESC`
	conn, err := s.ageConn(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	defer conn.Close()
	rows, err := conn.QueryContext(r.Context(), s.cypherSQL(cypher, "from_label agtype, edge_label agtype, to_label agtype, edge_count agtype"))
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()

	var summaries []edgeSummary
	for rows.Next() {
		var fromLabel, edgeLabel, toLabel, count string
		if err := rows.Scan(&fromLabel, &edgeLabel, &toLabel, &count); err != nil {
			writeError(w, err)
			return
		}
		summaries = append(summaries, edgeSummary{
			FromLabel: trimAgtypeString(fromLabel),
			EdgeLabel: trimAgtypeString(edgeLabel),
			ToLabel:   trimAgtypeString(toLabel),
			Count:     trimAgtypeString(count),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, summaries)
}

func (s *server) cypherSQL(cypher, columns string) string {
	return fmt.Sprintf("SELECT * FROM cypher(%s, %s) AS (%s)", sqlString(s.graphName), dollarQuote(cypher), columns)
}

func canAddTriple(seen map[string]struct{}, from, to vertexValue, maxNodes int) bool {
	needed := 0
	if _, ok := seen[from.ID.String()]; !ok {
		needed++
	}
	if _, ok := seen[to.ID.String()]; !ok && to.ID.String() != from.ID.String() {
		needed++
	}
	return len(seen)+needed <= maxNodes
}

func addNode(result *graphResponse, seen map[string]struct{}, v vertexValue, maxNodes int) {
	id := v.ID.String()
	if _, ok := seen[id]; ok {
		return
	}
	if len(seen) >= maxNodes {
		return
	}
	seen[id] = struct{}{}
	name, _ := stringProperty(v.Properties, "name")
	content, _ := stringProperty(v.Properties, "content")
	kind := metadataKind(v.Properties)
	if name == "" {
		name = v.Label + " " + id
	}
	result.Nodes = append(result.Nodes, node{
		ID:         id,
		Label:      v.Label,
		Name:       name,
		Kind:       kind,
		Content:    content,
		Properties: v.Properties,
	})
}

func addEdge(result *graphResponse, seen map[string]struct{}, e edgeValue) {
	id := e.ID.String()
	if _, ok := seen[id]; ok {
		return
	}
	seen[id] = struct{}{}
	result.Edges = append(result.Edges, edge{
		ID:         id,
		From:       e.StartID.String(),
		To:         e.EndID.String(),
		Label:      e.Label,
		Properties: e.Properties,
	})
}

func parseVertex(raw string) (vertexValue, error) {
	var v vertexValue
	if err := decodeAgtype(raw, "::vertex", &v); err != nil {
		return vertexValue{}, err
	}
	if v.Properties == nil {
		v.Properties = map[string]any{}
	}
	return v, nil
}

func parseEdge(raw string) (edgeValue, error) {
	var e edgeValue
	if err := decodeAgtype(raw, "::edge", &e); err != nil {
		return edgeValue{}, err
	}
	if e.Properties == nil {
		e.Properties = map[string]any{}
	}
	return e, nil
}

func decodeAgtype(raw, suffix string, dst any) error {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, suffix)
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("decode agtype %q: %w", raw, err)
	}
	return nil
}

func stringProperty(properties map[string]any, key string) (string, bool) {
	value, ok := properties[key]
	if !ok || value == nil {
		return "", false
	}
	switch v := value.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	default:
		return fmt.Sprint(v), true
	}
}

func metadataKind(properties map[string]any) string {
	raw, ok := properties["metadata"].(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"trpc_ast_type", "kind", "trpc_ast_scope"} {
		if value, ok := stringProperty(raw, key); ok && value != "" {
			return value
		}
	}
	return ""
}

func queryInt(r *http.Request, key string, fallback, max int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}

func cypherStringList(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, cypherString(value))
	}
	return strings.Join(parts, ", ")
}

func cypherString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `\'`)
	return "'" + value + "'"
}

func sqlString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func dollarQuote(value string) string {
	tag := "$cypher$"
	if !strings.Contains(value, tag) {
		return tag + value + tag
	}
	return "$cypher_query$" + value + "$cypher_query$"
}

func trimAgtypeString(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"`)
	return value
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	log.Printf("request error: %v", err)
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
