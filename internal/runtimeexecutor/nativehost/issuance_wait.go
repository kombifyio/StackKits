package nativehost

import (
	"context"
	"errors"
	"time"
)

// Certificate issuance is asynchronous by design: the router registers a
// route, then its ACME resolver (step-ca on Basement, Let's Encrypt on Cloud)
// issues the leaf a few seconds to minutes later. Verifying the leaf on the
// first handshake therefore raced issuance and failed a first apply on a
// fresh host (Cloud Kit VPS run 2026-09-14: "x509: certificate is valid for
// …traefik.default, not base.<domain>"). The bounded wait below retries the
// exact probe until the leaf verifies or the budget ends; it never relaxes
// what is verified.
const (
	certificateIssuanceWait     = 3 * time.Minute
	certificateIssuanceInterval = 5 * time.Second
)

// waitForCertificateIssuance runs attempt until it succeeds, the wait budget
// elapses, or the context ends. A zero wait performs exactly one attempt.
func waitForCertificateIssuance(ctx context.Context, wait, interval time.Duration, attempt func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = certificateIssuanceInterval
	}
	deadline := time.Now().Add(wait)
	for {
		err := attempt()
		if err == nil {
			return nil
		}
		var terminal interface{ issuanceTerminal() bool }
		if errors.As(err, &terminal) && terminal.issuanceTerminal() {
			return err
		}
		if wait <= 0 || !time.Now().Add(interval).Before(deadline) {
			return err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
}
