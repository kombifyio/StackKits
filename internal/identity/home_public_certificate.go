package identity

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/publiccert"
)

const (
	homePublicCertificateInterval = 12 * time.Hour
	// homePublicCertificateRouterRetry follows a router policy refresh that
	// could not read the router's routes (the router was restarting).
	homePublicCertificateRouterRetry = 10 * time.Minute
	// homePublicCertificateNoticeWindow is how close to its expiry a
	// certificate that is still not renewed raises the Owner notice.
	homePublicCertificateNoticeWindow = 14 * 24 * time.Hour
)

// keepHomePublicCertificateCurrent renews the public-names certificate of a
// public identity origin on the host, where the ACME account and the DNS
// token are: within 30 days of its expiry, through the DNS-01 provider the
// Owner configured (kombify Me or Cloudflare), and then reloads the router. A
// local-only node, or one that never issued one, renews nothing.
//
// Between renewal rounds it refreshes the router policy the moment the
// certificate stops serving, so the routes it covered fall back to step-ca
// without an apply, and it raises one Owner notice per certificate once the
// certificate is within 14 days of its expiry and still not renewed.
func keepHomePublicCertificateCurrent(ctx context.Context, root string) {
	nextRenewal := time.Now()
	for {
		now := time.Now()
		var err error
		if !now.Before(nextRenewal) {
			var renewed bool
			renewed, err = publiccert.Renew(ctx, root)
			if err != nil {
				slog.Warn("the public-names certificate was not renewed or the router policy not refreshed", "error", err)
			} else if renewed {
				slog.Info("the public-names certificate was renewed and the router reloaded")
			}
			nextRenewal = now.Add(homePublicCertificateInterval)
			notifyHomePublicCertificateRenewalFailing(root, now)
		} else if err = publiccert.RefreshRouter(ctx, root); err != nil {
			slog.Warn("the router policy was not refreshed", "error", err)
		}
		wait := time.Until(nextRenewal)
		if until, serves := localevidence.PublicRouterCertificateServesUntil(root, time.Now()); serves {
			wait = min(wait, time.Until(until)+time.Minute)
		}
		if errors.Is(err, publiccert.ErrRouterNotRefreshed) {
			wait = min(wait, homePublicCertificateRouterRetry)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(max(wait, time.Minute)):
		}
	}
}

// notifyHomePublicCertificateRenewalFailing records the renewal-failing
// notice through the recovery notices, which the Hub pushes to every Owner
// device; an Authority without recovery set up has no Owner notice channel
// and only logs it.
func notifyHomePublicCertificateRenewalFailing(root string, now time.Time) {
	expiresAt, failing := publiccert.RenewalFailing(root, now, homePublicCertificateNoticeWindow)
	if !failing {
		return
	}
	slog.Warn("the public-names certificate keeps failing to renew", "expiresAt", expiresAt)
	err := updateRecoveryState(root, false, func(state *recoveryState, _ bool) error {
		if slices.ContainsFunc(state.Notices, func(notice RecoveryNotice) bool {
			return notice.Kind == RecoveryNoticePublicCertificateRenewalFailing &&
				notice.CertificateExpiresAt != nil && notice.CertificateExpiresAt.Equal(expiresAt)
		}) {
			return errHomeNoticeRecorded
		}
		addRecoveryNotice(root, state, RecoveryNoticePublicCertificateRenewalFailing, "identity-signer", nil, now.UTC())
		state.Notices[len(state.Notices)-1].CertificateExpiresAt = &expiresAt
		return nil
	})
	switch {
	case err == nil, errors.Is(err, errHomeNoticeRecorded):
	case errors.Is(err, ErrRecoveryNotConfigured):
		slog.Warn("no Owner notice for the failing public-names certificate renewal: recovery is not set up on this Authority")
	default:
		slog.Warn("the Owner notice for the failing public-names certificate renewal was not recorded", "error", err)
	}
}

var errHomeNoticeRecorded = errors.New("identity: the notice is already recorded")
