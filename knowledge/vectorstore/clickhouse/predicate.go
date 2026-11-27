//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import "fmt"

// predicate is the body of a SQL WHERE clause together with the positional
// arguments it needs.
//
// Every predicate helper in this package returns one of these, so callers do
// not have to remember whether a helper yields a bare expression or a string
// already prefixed with " WHERE ". Only the code that assembles the final
// statement calls whereClause, which is where the prefix is added.
//
// Conditions whose values are inlined as SQL literals, such as metadata
// comparisons, leave args empty. Conditions whose values are bound as
// placeholders, such as IDs and keyword matches, append to it in the order the
// placeholders appear.
type predicate struct {
	sql  string
	args []any
}

// empty reports whether the predicate carries no condition at all.
func (p predicate) empty() bool {
	return p.sql == ""
}

// and combines the predicate with others using AND, appending their arguments
// in the same order as their SQL. Each side keeps its parentheses, so a
// top-level OR in any of them cannot escape its scope.
func (p predicate) and(others ...predicate) predicate {
	parts := make([]string, 0, 1+len(others))
	args := make([]any, 0, len(p.args))
	if !p.empty() {
		parts = append(parts, p.sql)
		args = append(args, p.args...)
	}
	for _, other := range others {
		if other.empty() {
			continue
		}
		parts = append(parts, other.sql)
		args = append(args, other.args...)
	}
	if len(parts) == 0 {
		return predicate{}
	}
	return predicate{sql: joinAnd(parts...), args: args}
}

// whereClause renders the predicate as a " WHERE ..." clause and returns the
// arguments it needs. The clause is empty when the predicate is empty, which
// lets callers skip a full scan instead of filtering on a constant true.
func (p predicate) whereClause() (string, []any) {
	if p.empty() {
		return "", nil
	}
	return " WHERE " + p.sql, p.args
}

// literalPredicate builds a predicate from an expression that already carries
// its values, so it needs no bound arguments.
func literalPredicate(expr string) predicate {
	if expr == "" {
		return predicate{}
	}
	return predicate{sql: expr}
}

// idPredicate builds an "id IN (?, ...)" predicate, binding the IDs as
// placeholders.
func (vs *VectorStore) idPredicate(ids []string) predicate {
	if len(ids) == 0 {
		return predicate{}
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	return predicate{
		sql:  fmt.Sprintf("%s IN (%s)", vs.option.idFieldName, joinList(placeholders)),
		args: args,
	}
}

// keywordPredicate builds the case-insensitive substring match used by keyword
// and hybrid search, binding the query text as a placeholder.
func (vs *VectorStore) keywordPredicate(query string) predicate {
	return predicate{
		sql:  fmt.Sprintf("positionCaseInsensitive(%s, ?) > 0", vs.option.contentFieldName),
		args: []any{query},
	}
}
