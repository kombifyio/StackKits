package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// The recommended release is the newest StackKits release on which every
// declared test lane passed (scripts/public/recommended-release.mjs). The
// release mirror publishes it live as recommended.json, the same document the
// installer reads, so this line never comes from a stale embedded copy.
const defaultRecommendedMirror = "https://assets.kombify.io/stackkits/releases"

var recommendedTagPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

type recommendedRelease struct {
	SchemaVersion  string  `json:"schemaVersion"`
	RecommendedTag *string `json:"recommended_tag"`
	NewestTag      *string `json:"newest_tag"`
}

func recommendedReleaseURL() string {
	mirror := strings.TrimRight(strings.TrimSpace(os.Getenv("STACKKIT_RELEASE_MIRROR_URL")), "/")
	if mirror == "" {
		mirror = defaultRecommendedMirror
	}
	return mirror + "/recommended.json"
}

func fetchRecommendedRelease(url string) (recommendedRelease, error) {
	var doc recommendedRelease
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return doc, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return doc, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&doc); err != nil {
		return doc, err
	}
	if doc.SchemaVersion != "stackkits.recommended-release/v1" {
		return doc, fmt.Errorf("unexpected schema %q", doc.SchemaVersion)
	}
	if doc.RecommendedTag != nil && !recommendedTagPattern.MatchString(*doc.RecommendedTag) {
		return doc, fmt.Errorf("invalid recommended tag %q", *doc.RecommendedTag)
	}
	return doc, nil
}

func printRecommendedRelease() {
	installed := "v" + strings.TrimPrefix(strings.TrimSpace(version), "v")
	if !recommendedTagPattern.MatchString(installed) {
		installed = fmt.Sprintf("a development build (%s)", strings.TrimSpace(version))
	}
	url := recommendedReleaseURL()
	doc, err := fetchRecommendedRelease(url)
	switch {
	case err != nil:
		fmt.Printf("  Recommended release:  %s (%s: %v)\n", yellow("unavailable"), url, err)
	case doc.RecommendedTag == nil:
		fmt.Printf("  Recommended release:  %s — no release has passed every declared test lane yet\n", yellow("none"))
	case *doc.RecommendedTag == installed:
		fmt.Printf("  Recommended release:  %s (installed)\n", green(*doc.RecommendedTag))
	default:
		fmt.Printf("  Recommended release:  %s (installed %s)\n", *doc.RecommendedTag, installed)
	}
}
