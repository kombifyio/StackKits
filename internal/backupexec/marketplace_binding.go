package backupexec

import (
	"context"
	"errors"
	"reflect"
	"slices"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// BindMarketplaceSources supplies observation only for already selected
// policy sources. Native Store control is deliberately absent from Docker's
// fallback methods; the central lifecycle uses its durable manager driver.
func BindMarketplaceSources(base ContainerQuiescer, selected []localbackuppolicy.MarketplaceService, observe func(context.Context, localbackuppolicy.MarketplaceService) (QuiesceContainer, error)) (ContainerQuiescer, error) {
	q, ok := base.(*dockerV2Quiescer)
	if !ok || observe == nil || len(selected) == 0 {
		return nil, errors.New("Store quiescence requires the existing Docker source verifier and exact selected source")
	}
	if !reflect.DeepEqual(q.storeSelections, selected) {
		return nil, errors.New("Store observer differs from the already selected policy source")
	}
	q.storeSelections = slices.Clone(selected)
	q.storeObserve = observe
	return q, nil
}

func (q *dockerV2Quiescer) validateStoreSources(ctx context.Context) error {
	for _, selected := range q.storeSelections {
		if q.storeObserve == nil {
			return errors.New("Store source has no native generation observer")
		}
		container, err := q.storeObserve(ctx, selected)
		if err != nil {
			return err
		}
		if container.ID != selected.ExpectedGeneration.ContainerID {
			return errors.New("Store source generation changed")
		}
	}
	return nil
}
