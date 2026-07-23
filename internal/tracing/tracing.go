package tracing

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/abyssmemes/contextverse/server"

// Provider holds an optional OTLP tracer. Empty endpoint → no-op (off).
type Provider struct {
	tp      *sdktrace.TracerProvider
	tracer  trace.Tracer
	enabled bool
}

// New builds a Provider. otlpEndpoint examples: "http://localhost:4318" or "localhost:4318".
func New(otlpEndpoint string) (*Provider, error) {
	ep := strings.TrimSpace(otlpEndpoint)
	if ep == "" {
		return &Provider{enabled: false, tracer: otel.Tracer(tracerName)}, nil
	}
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(normalizeEndpointURL(ep)),
	}
	exp, err := otlptracehttp.New(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("otlp http exporter: %w", err)
	}
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("contextd"),
		),
	)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return &Provider{
		tp:      tp,
		tracer:  tp.Tracer(tracerName),
		enabled: true,
	}, nil
}

func normalizeEndpointURL(ep string) string {
	if strings.HasPrefix(ep, "http://") || strings.HasPrefix(ep, "https://") {
		return strings.TrimRight(ep, "/")
	}
	return "http://" + strings.TrimRight(ep, "/")
}

// Enabled reports whether OTLP export is active.
func (p *Provider) Enabled() bool {
	return p != nil && p.enabled
}

// Shutdown flushes and closes the tracer provider (no-op when disabled).
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil || p.tp == nil {
		return nil
	}
	return p.tp.Shutdown(ctx)
}

// Middleware starts one span per request with attribute request_id (from ctx).
// Expects request_id already on context (withRequestID outer wraps this).
func (p *Provider) Middleware(requestIDFrom func(context.Context) string, next http.Handler) http.Handler {
	if !p.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := requestIDFrom(r.Context())
		ctx, span := p.tracer.Start(r.Context(), r.Method+" "+r.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("request_id", rid),
				attribute.String("http.method", r.Method),
				attribute.String("http.target", r.URL.Path),
			),
		)
		defer span.End()

		sw := &statusCapture{ResponseWriter: w, code: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r.WithContext(ctx))
		span.SetAttributes(
			attribute.Int("http.status_code", sw.code),
			attribute.Int64("http.duration_ms", time.Since(start).Milliseconds()),
		)
		if sw.code >= 500 {
			span.SetStatus(codes.Error, http.StatusText(sw.code))
		}
	})
}

type statusCapture struct {
	http.ResponseWriter
	code        int
	wroteHeader bool
}

func (w *statusCapture) WriteHeader(code int) {
	if !w.wroteHeader {
		w.code = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusCapture) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusCapture) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
