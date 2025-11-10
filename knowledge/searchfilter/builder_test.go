//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package searchfilter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEqual(t *testing.T) {
	cond := Equal("status", "active")
	assert.Equal(t, "status", cond.Field)
	assert.Equal(t, OperatorEqual, cond.Operator)
	assert.Equal(t, "active", cond.Value)
}

func TestNotEqual(t *testing.T) {
	cond := NotEqual("status", "inactive")
	assert.Equal(t, "status", cond.Field)
	assert.Equal(t, OperatorNotEqual, cond.Operator)
	assert.Equal(t, "inactive", cond.Value)
}

func TestGreaterThan(t *testing.T) {
	cond := GreaterThan("age", 18)
	assert.Equal(t, "age", cond.Field)
	assert.Equal(t, OperatorGreaterThan, cond.Operator)
	assert.Equal(t, 18, cond.Value)
}

func TestGreaterThanOrEqual(t *testing.T) {
	cond := GreaterThanOrEqual("score", 90.0)
	assert.Equal(t, "score", cond.Field)
	assert.Equal(t, OperatorGreaterThanOrEqual, cond.Operator)
	assert.Equal(t, 90.0, cond.Value)
}

func TestLessThan(t *testing.T) {
	cond := LessThan("price", 100)
	assert.Equal(t, "price", cond.Field)
	assert.Equal(t, OperatorLessThan, cond.Operator)
	assert.Equal(t, 100, cond.Value)
}

func TestLessThanOrEqual(t *testing.T) {
	cond := LessThanOrEqual("quantity", 50)
	assert.Equal(t, "quantity", cond.Field)
	assert.Equal(t, OperatorLessThanOrEqual, cond.Operator)
	assert.Equal(t, 50, cond.Value)
}

func TestIn(t *testing.T) {
	cond := In("category", "electronics", "books", "toys")
	assert.Equal(t, "category", cond.Field)
	assert.Equal(t, OperatorIn, cond.Operator)
	assert.Equal(t, []any{"electronics", "books", "toys"}, cond.Value)
}

func TestNotIn(t *testing.T) {
	cond := NotIn("status", "deleted", "archived")
	assert.Equal(t, "status", cond.Field)
	assert.Equal(t, OperatorNotIn, cond.Operator)
	assert.Equal(t, []any{"deleted", "archived"}, cond.Value)
}

func TestLike(t *testing.T) {
	cond := Like("name", "%john%")
	assert.Equal(t, "name", cond.Field)
	assert.Equal(t, OperatorLike, cond.Operator)
	assert.Equal(t, "%john%", cond.Value)
}

func TestNotLike(t *testing.T) {
	cond := NotLike("email", "%@spam.com")
	assert.Equal(t, "email", cond.Field)
	assert.Equal(t, OperatorNotLike, cond.Operator)
	assert.Equal(t, "%@spam.com", cond.Value)
}

func TestBetween(t *testing.T) {
	cond := Between("age", 18, 65)
	assert.Equal(t, "age", cond.Field)
	assert.Equal(t, OperatorBetween, cond.Operator)
	assert.Equal(t, []any{18, 65}, cond.Value)
}

func TestAnd(t *testing.T) {
	cond := And(
		Equal("status", "active"),
		GreaterThan("age", 18),
	)
	assert.Equal(t, OperatorAnd, cond.Operator)
	conditions := cond.Value.([]*UniversalFilterCondition)
	assert.Len(t, conditions, 2)
	assert.Equal(t, "status", conditions[0].Field)
	assert.Equal(t, "age", conditions[1].Field)
}

func TestOr(t *testing.T) {
	cond := Or(
		Equal("status", "active"),
		Equal("status", "pending"),
	)
	assert.Equal(t, OperatorOr, cond.Operator)
	conditions := cond.Value.([]*UniversalFilterCondition)
	assert.Len(t, conditions, 2)
	assert.Equal(t, "status", conditions[0].Field)
	assert.Equal(t, "status", conditions[1].Field)
}

func TestComplexCondition(t *testing.T) {
	// (status = "active" AND age > 18) OR (status = "premium" AND age > 21)
	cond := Or(
		And(
			Equal("status", "active"),
			GreaterThan("age", 18),
		),
		And(
			Equal("status", "premium"),
			GreaterThan("age", 21),
		),
	)

	assert.Equal(t, OperatorOr, cond.Operator)
	orConditions := cond.Value.([]*UniversalFilterCondition)
	assert.Len(t, orConditions, 2)

	// First AND condition
	firstAnd := orConditions[0]
	assert.Equal(t, OperatorAnd, firstAnd.Operator)
	firstAndConditions := firstAnd.Value.([]*UniversalFilterCondition)
	assert.Len(t, firstAndConditions, 2)
	assert.Equal(t, "status", firstAndConditions[0].Field)
	assert.Equal(t, "active", firstAndConditions[0].Value)
	assert.Equal(t, "age", firstAndConditions[1].Field)
	assert.Equal(t, 18, firstAndConditions[1].Value)

	// Second AND condition
	secondAnd := orConditions[1]
	assert.Equal(t, OperatorAnd, secondAnd.Operator)
	secondAndConditions := secondAnd.Value.([]*UniversalFilterCondition)
	assert.Len(t, secondAndConditions, 2)
	assert.Equal(t, "status", secondAndConditions[0].Field)
	assert.Equal(t, "premium", secondAndConditions[0].Value)
	assert.Equal(t, "age", secondAndConditions[1].Field)
	assert.Equal(t, 21, secondAndConditions[1].Value)
}

func TestNestedConditions(t *testing.T) {
	// status = "active" AND (age > 18 OR score >= 90)
	cond := And(
		Equal("status", "active"),
		Or(
			GreaterThan("age", 18),
			GreaterThanOrEqual("score", 90),
		),
	)

	assert.Equal(t, OperatorAnd, cond.Operator)
	andConditions := cond.Value.([]*UniversalFilterCondition)
	assert.Len(t, andConditions, 2)

	// First condition: status = "active"
	assert.Equal(t, "status", andConditions[0].Field)
	assert.Equal(t, OperatorEqual, andConditions[0].Operator)

	// Second condition: (age > 18 OR score >= 90)
	orCond := andConditions[1]
	assert.Equal(t, OperatorOr, orCond.Operator)
	orConditions := orCond.Value.([]*UniversalFilterCondition)
	assert.Len(t, orConditions, 2)
	assert.Equal(t, "age", orConditions[0].Field)
	assert.Equal(t, OperatorGreaterThan, orConditions[0].Operator)
	assert.Equal(t, "score", orConditions[1].Field)
	assert.Equal(t, OperatorGreaterThanOrEqual, orConditions[1].Operator)
}

func TestMultipleConditions(t *testing.T) {
	// Test with multiple conditions in AND
	cond := And(
		Equal("status", "active"),
		GreaterThan("age", 18),
		LessThan("age", 65),
		In("category", "A", "B", "C"),
	)

	assert.Equal(t, OperatorAnd, cond.Operator)
	conditions := cond.Value.([]*UniversalFilterCondition)
	assert.Len(t, conditions, 4)
}

func TestBetweenWithFloats(t *testing.T) {
	cond := Between("price", 10.5, 99.99)
	assert.Equal(t, "price", cond.Field)
	assert.Equal(t, OperatorBetween, cond.Operator)
	values := cond.Value.([]any)
	assert.Equal(t, 10.5, values[0])
	assert.Equal(t, 99.99, values[1])
}

