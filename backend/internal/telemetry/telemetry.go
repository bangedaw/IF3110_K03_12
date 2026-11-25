// Package telemetry memasang OpenTelemetry untuk proses ini dan menyambungkan log dengan trace.
package telemetry

import (
	"context"
	"fmt"
	"io"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	ExporterConsole = "console"
	ExporterNone    = "none"
)

// Setup memasang TracerProvider dan propagator global. Exporter console menulis setiap span sebagai satu
// baris JSON ke w. Fungsi yang dikembalikan mengirim span yang masih tertahan di buffer, jadi panggil saat
// proses berhenti.
func Setup(service, exporter string, w io.Writer) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	switch exporter {
	case ExporterNone:
		return func(context.Context) error { return nil }, nil
	case ExporterConsole, "":
	default:
		return nil, fmt.Errorf("OTEL_TRACES_EXPORTER %q tidak dikenal, pilih %q atau %q", exporter, ExporterConsole, ExporterNone)
	}
	exp, err := stdouttrace.New(stdouttrace.WithWriter(w))
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(attribute.String("service.name", service)))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
