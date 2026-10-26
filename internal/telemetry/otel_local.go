package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/dataprofilev1"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
)

// Full Local changes the exporter transport, not instrumentation or resources.
func newLocalTraceExporter(ctx context.Context, runtime OTelRuntime) (sdktrace.SpanExporter, error) {
	protocol := strings.ToLower(strings.TrimSpace(runtime.Protocol))
	switch protocol {
	case "", "grpc", "http", "http/protobuf":
	default:
		return nil, errors.New("invalid Full Local OTLP protocol: use grpc or http/protobuf")
	}
	endpoint, err := localOTLPEndpoint(runtime.Endpoint)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Resolver:  dataprofile.LocalResolver(),
	}
	if protocol == "" || protocol == "grpc" {
		port := endpoint.Port()
		if port == "" {
			port = "443"
			if endpoint.Scheme == "http" {
				port = "80"
			}
		}
		guarded := dataprofile.GuardDial(dialer.DialContext)
		return otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpointURL(endpoint.String()),
			// Passthrough leaves DNS to GuardDial's LocalResolver instead of
			// gRPC's independent DNS resolver. Keep exporter-owned shutdown.
			otlptracegrpc.WithEndpoint("passthrough:///"+net.JoinHostPort(endpoint.Hostname(), port)),
			otlptracegrpc.WithHeaders(runtime.Headers),
			otlptracegrpc.WithTimeout(5*time.Second),
			otlptracegrpc.WithDialOption(
				grpc.WithNoProxy(),
				grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
					return guarded(ctx, "tcp", address)
				}),
			),
		)
	}
	client, err := localOTLPHTTPClient(dialer)
	if err != nil {
		return nil, err
	}
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint.String()),
		otlptracehttp.WithHeaders(runtime.Headers),
		otlptracehttp.WithTimeout(5*time.Second),
		otlptracehttp.WithHTTPClient(client),
	)
	if err != nil {
		client.CloseIdleConnections()
		return nil, err
	}
	return &localHTTPTraceExporter{SpanExporter: exporter, client: client}, nil
}

type localHTTPTraceExporter struct {
	sdktrace.SpanExporter
	client *http.Client
}

func (e *localHTTPTraceExporter) Shutdown(ctx context.Context) error {
	defer e.client.CloseIdleConnections()
	return e.SpanExporter.Shutdown(ctx)
}

func localOTLPEndpoint(raw string) (*url.URL, error) {
	endpoint, err := url.Parse(raw)
	// Never echo a rejected URL: it may carry credentials or payload in a query.
	if err != nil || endpoint.Opaque != "" || endpoint.Hostname() == "" ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" ||
		strings.HasSuffix(endpoint.Host, ":") {
		return nil, errors.New("invalid Full Local OTLP endpoint: use an http(s) collector URL without credentials, query or fragment")
	}
	if port := endpoint.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, errors.New("invalid Full Local OTLP endpoint port")
		}
	}
	if addr, err := netip.ParseAddr(endpoint.Hostname()); err == nil && !dataprofile.LocalAddr(addr) {
		return nil, &dataprofile.DeniedError{Host: endpoint.Hostname()}
	}
	return endpoint, nil
}

func localOTLPHTTPClient(dialer *net.Dialer) (*http.Client, error) {
	tlsConfig := localOTLPHTTPClientTLS()
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSClientConfig:       tlsConfig,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	dataprofile.GuardTransport(transport)
	return &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
		// A collector is the configured destination. Do not forward trace
		// bodies or collector credentials to a redirect's destination.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// WithHTTPClient bypasses the SDK's TLS transport options. Preserve its
// ordered valid CA pools and complete client pairs on the guarded transport.
// Invalid overrides retain earlier valid material for the same collector;
// the SDK constructor still reports environment-loading failures.
func localOTLPHTTPClientTLS() *tls.Config {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	for _, prefix := range []string{"OTEL_EXPORTER_OTLP_", "OTEL_EXPORTER_OTLP_TRACES_"} {
		if file := strings.TrimSpace(os.Getenv(prefix + "CERTIFICATE")); file != "" {
			if pem, err := os.ReadFile(file); err == nil {
				pool := x509.NewCertPool()
				if pool.AppendCertsFromPEM(pem) {
					config.RootCAs = pool
				}
			}
		}
		certificate := strings.TrimSpace(os.Getenv(prefix + "CLIENT_CERTIFICATE"))
		key := strings.TrimSpace(os.Getenv(prefix + "CLIENT_KEY"))
		if certificate == "" || key == "" {
			continue
		}
		if pair, err := tls.LoadX509KeyPair(certificate, key); err == nil {
			config.Certificates = []tls.Certificate{pair}
		}
	}
	return config
}
