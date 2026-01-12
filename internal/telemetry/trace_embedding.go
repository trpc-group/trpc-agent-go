//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

// EmbeddingAttributes represents the attributes of an embedding call.
type EmbeddingAttributes struct {
	RequestEncodingFormat *string
	RequestModel          string
	Dimensions            int
	Error                 error
	InputToken            *int64
	Request               *string
	Response              *string
	ServerAddress         *string
	ServerPort            *int
}

// TraceEmbedding traces the invocation of an embedding call.
func TraceEmbedding(span trace.Span, embeddingAttributes *EmbeddingAttributes) {
	span.SetAttributes(buildEmbeddingAttributes(embeddingAttributes)...)
	if embeddingAttributes.Error != nil {
		span.SetStatus(codes.Error, embeddingAttributes.Error.Error())
	}
}

func buildEmbeddingAttributes(embeddingAttributes *EmbeddingAttributes) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(semconvtrace.KeyGenAIOperationName, OperationEmbeddings),
		attribute.String(semconvtrace.KeyGenAIRequestModel, embeddingAttributes.RequestModel),
		attribute.Int(semconvtrace.KeyGenAIEmbeddingsDimensionCount, embeddingAttributes.Dimensions),
	}
	if embeddingAttributes.RequestEncodingFormat != nil {
		attrs = append(attrs, attribute.StringSlice(semconvtrace.KeyGenAIRequestEncodingFormats, []string{*embeddingAttributes.RequestEncodingFormat}))
	}
	if embeddingAttributes.InputToken != nil {
		attrs = append(attrs, attribute.Int64(semconvtrace.KeyGenAIUsageInputTokens, *embeddingAttributes.InputToken))
	}
	if embeddingAttributes.Request != nil {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAIEmbeddingsRequest, *embeddingAttributes.Request))
	}
	if embeddingAttributes.Response != nil {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAIEmbeddingsResponse, *embeddingAttributes.Response))
	}
	if embeddingAttributes.Error != nil {
		attrs = append(attrs, attribute.String(semconvtrace.KeyErrorType, semconvtrace.ValueDefaultErrorType), attribute.String(semconvtrace.KeyErrorMessage, embeddingAttributes.Error.Error()))
	}
	if embeddingAttributes.ServerAddress != nil {
		attrs = append(attrs, attribute.String(semconvtrace.KeyServerAddress, *embeddingAttributes.ServerAddress))
	}
	if embeddingAttributes.ServerPort != nil {
		attrs = append(attrs, attribute.Int(semconvtrace.KeyServerPort, *embeddingAttributes.ServerPort))
	}
	return attrs
}
