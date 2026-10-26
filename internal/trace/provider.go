// Package trace 提供 OTLP 链路初始化（ClickStack）。
//
// 与其它 AIS 服务同口径：未配置 OTEL_EXPORTER_OTLP_ENDPOINT 时为 no-op，
// 初始化失败不阻断启动，认证头通过 OTEL_EXPORTER_OTLP_HEADERS 传入。
package trace

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// NewProvider 初始化并注册全局 TracerProvider。
func NewProvider(ctx context.Context) (*sdktrace.TracerProvider, error) {
	provider, err := buildProvider(ctx)
	if err != nil {
		return nil, err
	}
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return provider, nil
}

func buildProvider(ctx context.Context) (*sdktrace.TracerProvider, error) {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		return sdktrace.NewTracerProvider(), nil
	}

	opts := []otlptracehttp.Option{otlptracehttp.WithInsecure()}
	if strings.HasPrefix(endpoint, "http://") {
		endpoint = strings.TrimPrefix(endpoint, "http://")
	} else {
		endpoint = strings.TrimPrefix(endpoint, "https://")
		opts = []otlptracehttp.Option{}
	}
	opts = append(opts, otlptracehttp.WithEndpoint(endpoint))
	if headers := parseHeaders(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")); len(headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(headers))
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create otlp trace exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", "axonhub"),
			attribute.String("deployment.environment", envOr("AIS_ENV", "prod2")),
			attribute.String("service.version", envOr("AIS_IMAGE_TAG", "unknown")),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build resource: %w", err)
	}

	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	), nil
}

func parseHeaders(raw string) map[string]string {
	headers := map[string]string{}
	for _, item := range strings.Split(raw, ",") {
		if key, value, ok := strings.Cut(item, "="); ok {
			headers[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return headers
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
