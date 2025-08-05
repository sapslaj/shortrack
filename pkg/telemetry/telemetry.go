package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/go-slog/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/sapslaj/shortrack/pkg/env"
)

type ContextKey string

const LoggerContextKey ContextKey = "sapslaj.shortrack.logger"

func LogLevelFromEnv() slog.Level {
	s, err := env.GetDefault("SHORTRACK_LOG_LEVEL", "INFO")
	if err != nil {
		err = fmt.Errorf("could not parse SHORTRACK_LOG_LEVEL: %w", err)
	}
	s = strings.ToUpper(s)
	switch s {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO", "":
		return slog.LevelInfo
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		err = errors.Join(err, fmt.Errorf("invalid SHORTRACK_LOG_LEVEL: %q", s))
	}
	bootstrapLogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	bootstrapLogger.Error("error encountered parsing log level, defaulting to INFO", slog.Any("error", err))
	return slog.LevelInfo
}

func SlogJSON(key string, value any) slog.Attr {
	data, err := json.Marshal(value)
	if err != nil {
		return slog.String(key, "err!"+err.Error())
	}
	return slog.String(key, string(data))
}

func NewLogger() *slog.Logger {
	return slog.New(
		otelslog.NewHandler(
			slog.NewTextHandler(
				os.Stderr,
				&slog.HandlerOptions{
					AddSource: true,
					Level:     LogLevelFromEnv(),
				},
			),
		),
	)
}

func ContextWithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	if ctx == nil {
		ctx = context.TODO()
	}
	if logger == nil {
		logger = NewLogger()
	}
	return context.WithValue(ctx, LoggerContextKey, logger)
}

func LoggerFromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		ctx = context.TODO()
	}
	logger, ok := ctx.Value(LoggerContextKey).(*slog.Logger)
	if !ok || logger == nil {
		return NewLogger()
	}
	return logger
}

type TelemetryStuff struct {
	Context      context.Context
	Logger       *slog.Logger
	OtlpExporter *otlptrace.Exporter
}

func (ts *TelemetryStuff) Shutdown() {
	ts.Logger.Debug("stopping telemetry")
	if ts.OtlpExporter != nil {
		ts.Logger.Debug("stopping OTLP exporter")
		time.Sleep(10 * time.Second)
		err := ts.OtlpExporter.Shutdown(ts.Context)
		if err != nil {
			ts.Logger.Error("error shutting down OTLP exporter", slog.Any("error", err))
		}
		ts.Logger.Debug("OTLP exporter stopped")
	}
}

func StartTelemetry(ctx context.Context) *TelemetryStuff {
	ts := &TelemetryStuff{
		Context: ctx,
		Logger:  NewLogger(),
	}

	otlpEndpoint, err := env.GetDefault("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	if err != nil {
		ts.Logger.Error("error getting otel OTLP endpoint", slog.Any("error", err))
	}
	if otlpEndpoint == "" {
		ts.Logger.Debug("telemetry disabled")
	} else {
		ts.Logger = ts.Logger.With(slog.String("otlp_endpoint", otlpEndpoint))
		ts.Logger.Info("telemetry enabled")
		ts.OtlpExporter, err = otlptrace.New(
			ts.Context,
			otlptracegrpc.NewClient(
				otlptracegrpc.WithInsecure(),
				otlptracegrpc.WithEndpoint(otlpEndpoint),
			),
		)
		if err != nil {
			ts.Logger.Error("error setting up otlptrace", slog.Any("error", err))
			return ts
		}

		res, err := resource.New(
			context.Background(),
			resource.WithAttributes(
				attribute.String("service.name", "shortrack"),
				attribute.String("library.language", "go"),
			),
		)
		if err != nil {
			ts.Logger.Error("error setting up otel resource", slog.Any("error", err))
			return ts
		}

		otel.SetTracerProvider(
			sdktrace.NewTracerProvider(
				sdktrace.WithSampler(sdktrace.AlwaysSample()),
				sdktrace.WithBatcher(ts.OtlpExporter),
				sdktrace.WithResource(res),
			),
		)
	}

	return ts
}

// OtelJSON JSON marshals any value into an otel attribute.KeyValue
func OtelJSON(key string, value any) attribute.KeyValue {
	data, err := json.Marshal(value)
	if err != nil {
		return attribute.String(key, "err!"+err.Error())
	}
	return attribute.String(key, string(data))
}

func Error(err error) slog.Attr {
	return slog.Any("error", err)
}
