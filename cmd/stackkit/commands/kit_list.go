package commands

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"text/tabwriter"

	"github.com/kombifyio/stackkits/internal/productkits"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/spf13/cobra"
)

var (
	kitListChannel string
	kitListJSON    bool
)

var kitListCmd = &cobra.Command{
	Use:   "list",
	Short: "List published StackKits releases from GitHub",
	Long: `Resolve the newest published release for every public StackKit on the
current operating system and architecture. The release index is fetched from
the public kombifyio/stackKits GitHub Releases repository.

Channels:
  stable  newest release that is not a prerelease
  beta    newest -beta.* prerelease
  edge    newest -edge.* prerelease`,
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	RunE:        runKitList,
}

func init() {
	kitListCmd.Flags().StringVar(&kitListChannel, "channel", "stable", "Release channel: stable, beta, or edge.")
	kitListCmd.Flags().BoolVar(&kitListJSON, "json", false, "Emit stackkit.command-result/v1 JSON.")
	kitCmd.AddCommand(kitListCmd)
}

type publishedKitRelease struct {
	Kit           string                `json:"kit"`
	Version       string                `json:"version"`
	Channel       releaseindex.Channel  `json:"channel"`
	Platform      releaseindex.Platform `json:"platform"`
	Asset         string                `json:"asset"`
	ArchiveSHA256 string                `json:"archiveSha256"`
}

type releaseListingSnapshot struct {
	releaseindex.Source
	releases []releaseindex.Release
}

func (snapshot releaseListingSnapshot) ListReleases(context.Context) ([]releaseindex.Release, error) {
	return append([]releaseindex.Release(nil), snapshot.releases...), nil
}

func runKitList(cmd *cobra.Command, args []string) error {
	target, err := normalizePublicChannel(kitListChannel)
	if err != nil {
		return err
	}
	source := newPublicReleaseSource()
	releases, err := source.ListReleases(cmd.Context())
	if err != nil {
		return fmt.Errorf("list GitHub releases: %w", err)
	}
	resolver := releaseindex.Resolver{
		Source:       releaseListingSnapshot{Source: source, releases: releases},
		Attestations: newPublicAttestationVerifier(),
	}
	rows := make([]publishedKitRelease, 0, len(productkits.Slugs()))
	for _, kit := range productkits.Slugs() {
		resolution, resolveErr := resolver.Resolve(cmd.Context(), releaseindex.ResolveRequest{
			Kit: kit, Target: target, OS: runtime.GOOS, Arch: runtime.GOARCH,
		})
		if errors.Is(resolveErr, releaseindex.ErrNoRelease) {
			continue
		}
		if resolveErr != nil {
			return fmt.Errorf("resolve %s release: %w", kit, resolveErr)
		}
		rows = append(rows, publishedKitRelease{
			Kit: kit, Version: resolution.Asset.Version, Channel: resolution.Asset.Channel,
			Platform: resolution.Asset.Platform, Asset: resolution.Asset.Archive.Name,
			ArchiveSHA256: resolution.Asset.Archive.SHA256,
		})
	}
	if len(rows) == 0 {
		return fmt.Errorf("%w for channel=%s platform=%s/%s", releaseindex.ErrNoRelease, target, runtime.GOOS, runtime.GOARCH)
	}
	if kitListJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), rows)
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIT\tVERSION\tCHANNEL\tPLATFORM\tASSET\tSHA-256")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s/%s\t%s\t%s\n",
			row.Kit, row.Version, row.Channel, row.Platform.OS, row.Platform.Arch, row.Asset, shortDigest(row.ArchiveSHA256))
	}
	return tw.Flush()
}

func normalizePublicChannel(value string) (string, error) {
	switch releaseindex.Channel(value) {
	case releaseindex.ChannelStable, releaseindex.ChannelBeta, releaseindex.ChannelEdge:
		return value, nil
	default:
		return "", fmt.Errorf("--channel must be stable, beta, or edge")
	}
}
