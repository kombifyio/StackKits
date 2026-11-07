package identity

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/kombifyio/stackkits/internal/lancert"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/mereach"
)

const (
	homeLANCertificateInterval = 12 * time.Hour
	// homeLANAddressRetry follows a LAN address Me did not publish (a tunnel
	// teardown removed it, or Me was reconciling an earlier write) without
	// asking the ACME issuer anything.
	homeLANAddressRetry = 10 * time.Minute
)

// keepHomeLANCertificateCurrent renews the LAN-name certificate (homelab
// identity target §5) on the host, where the Me registration and the ACME
// account key are: within 30 days of its expiry, through kombify Me's DNS-01
// responder. Between renewals it republishes the LAN name's private address
// at Me, which removes that record with every tunnel teardown and which a
// moved site address outdates. A node that never issued one renews nothing.
func keepHomeLANCertificateCurrent(ctx context.Context, root string) {
	for {
		wait := homeLANCertificateInterval
		if renewed, err := renewHomeLANCertificate(ctx, root); err != nil {
			slog.Warn("the LAN-name certificate or its LAN address was not kept current", "error", err)
			if errors.Is(err, lancert.ErrAddressNotPublished) {
				wait = homeLANAddressRetry
			}
		} else if renewed {
			slog.Info("the LAN-name certificate was renewed")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func renewHomeLANCertificate(ctx context.Context, root string) (bool, error) {
	authority, err := homeAuthority(root)
	if err != nil {
		return false, nil
	}
	address, err := authority.IdentityRuntimeAddress()
	if err != nil {
		return false, nil
	}
	if _, isKombifyMe := mereach.BaseOf(address.Domain); !isKombifyMe {
		return false, nil
	}
	if _, _, err := lancert.Load(root); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	// A node that holds a LAN certificate renews it; a registration that is
	// gone or unreadable is a failure the signer logs, never a silent stop.
	registration, err := mereach.LoadRegistration(root)
	if err != nil {
		return false, err
	}
	uid, gid, err := localevidence.StackKitServerOwner(root)
	if err != nil {
		return false, err
	}
	return lancert.Renew(ctx, root, address.Domain, mereach.Client{Registration: registration}, uid, gid)
}
