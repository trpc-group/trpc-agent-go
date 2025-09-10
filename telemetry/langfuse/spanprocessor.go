package langfuse

import sdktrace "go.opentelemetry.io/otel/sdk/trace"

func newSpanProcessor(e sdktrace.SpanExporter) sdktrace.SpanProcessor {
	return sdktrace.NewBatchSpanProcessor(e)
}
