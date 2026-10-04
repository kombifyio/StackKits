package contentbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// The Immich endpoints the bridge may call. Each returns counts only, and
// each has its own narrow API key permission (Immich 2.7):
//
//	GET /api/assets/statistics    asset.statistics
//	GET /api/albums/statistics    album.statistics
//	GET /api/memories/statistics  memory.statistics
//
// No endpoint that returns a list, an id, a name, a file name, a location, a
// person or bytes is on the list, so the summary tier cannot leak one even
// through a defect above this layer.
const (
	immichAssetStatisticsPath  = "/api/assets/statistics"
	immichAlbumStatisticsPath  = "/api/albums/statistics"
	immichMemoryStatisticsPath = "/api/memories/statistics"
)

// ImmichPermissions are the API key permissions the bridge needs for the
// summary tier, and nothing more. The Immich 2.7 permission enum offers a
// statistics permission for each of the three counts, so the key can neither
// list nor open an asset, an album or a memory, and cannot write.
func ImmichPermissions() []string {
	return []string{"asset.statistics", "album.statistics", "memory.statistics"}
}

// ImmichEndpoints is the complete GET allowlist toward Immich.
func ImmichEndpoints() []Endpoint {
	return []Endpoint{
		// Timeline visibility keeps archived, hidden and locked assets out of
		// every count (standard section 2, "never part of any tier").
		{Path: immichAssetStatisticsPath, Query: []string{"visibility"}},
		{Path: immichAlbumStatisticsPath},
		{Path: immichMemoryStatisticsPath, Query: []string{"type", "for"}},
	}
}

// ImmichAdapter answers the Photos use case from Immich.
type ImmichAdapter struct {
	client *ReadOnlyClient
	now    func() time.Time
}

// NewImmichAdapter wires the adapter to its read-only client. now may be nil.
func NewImmichAdapter(client *ReadOnlyClient, now func() time.Time) *ImmichAdapter {
	if now == nil {
		now = time.Now
	}
	return &ImmichAdapter{client: client, now: now}
}

// UseCase is the contract use case this adapter answers.
func (*ImmichAdapter) UseCase() UseCase { return UseCasePhotos }

// Ceiling is the highest tier the node can answer for Photos. Previews need
// thumbnails and titles, which arrive with the node-signed thumbnail slice.
// Until then a previews request is answered at the summary tier.
func (*ImmichAdapter) Ceiling() Tier { return TierSummary }

// Counts reads the Photos counts. Assets are required: without them the
// answer is an error and never an empty set. Albums and on-this-day are
// reported when Immich answers them and omitted otherwise.
func (a *ImmichAdapter) Counts(ctx context.Context) (any, error) {
	var (
		wg               sync.WaitGroup
		assets           assetStatistics
		albums           albumStatistics
		memories         memoryStatistics
		assetsErr        error
		albumsErr        error
		memoriesErr      error
		memoryQuery      = url.Values{"type": {"on_this_day"}, "for": {formatTime(a.now())}}
		assetCountsQuery = url.Values{"visibility": {"timeline"}}
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		assetsErr = a.client.GetJSON(ctx, immichAssetStatisticsPath, assetCountsQuery, &assets)
	}()
	go func() {
		defer wg.Done()
		albumsErr = a.client.GetJSON(ctx, immichAlbumStatisticsPath, nil, &albums)
	}()
	go func() {
		defer wg.Done()
		memoriesErr = a.client.GetJSON(ctx, immichMemoryStatisticsPath, memoryQuery, &memories)
	}()
	wg.Wait()

	if assetsErr != nil {
		return nil, assetsErr
	}
	if assets.Images == nil || assets.Videos == nil || assets.Total == nil {
		return nil, fmt.Errorf("%w: unexpected answer shape", ErrAppUnreachable)
	}
	counts := &PhotosCounts{Assets: assets.Total.ptr(), Images: assets.Images.ptr(), Videos: assets.Videos.ptr()}
	if albumsErr == nil && albums.Owned != nil {
		counts.Albums = albums.Owned.ptr()
	}
	if memoriesErr == nil && memories.Total != nil {
		counts.OnThisDay = memories.Total.ptr()
	}
	return counts, nil
}

type assetStatistics struct {
	Images *count `json:"images"`
	Videos *count `json:"videos"`
	Total  *count `json:"total"`
}

type albumStatistics struct {
	Owned *count `json:"owned"`
}

type memoryStatistics struct {
	Total *count `json:"total"`
}

// count decodes a non-negative integer that Immich may serialize as a number
// or, from a database count, as a quoted number.
type count int64

func (c *count) ptr() *int64 {
	value := int64(*c)
	return &value
}

func (c *count) UnmarshalJSON(data []byte) error {
	var text string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
	} else {
		text = string(data)
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return errors.New("count is not an integer")
	}
	if value < 0 {
		return errors.New("count is negative")
	}
	*c = count(value)
	return nil
}
