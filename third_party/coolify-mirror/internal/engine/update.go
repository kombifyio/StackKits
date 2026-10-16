package engine

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// LatestRelease returns the newest released version on GitHub ("1.5.0"),
// read from the redirect of /releases/latest (no API rate limit).
func LatestRelease(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, RepoURL+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/releases/tag/") {
		return "", fmt.Errorf("unexpected answer from GitHub (%s)", resp.Status)
	}
	return strings.TrimPrefix(path.Base(loc), "v"), nil
}

// NewerRelease returns the latest release when it is newer than this one.
func NewerRelease(ctx context.Context) string {
	if Pinned {
		return ""
	}
	v, err := LatestRelease(ctx)
	if err != nil || CompareVersions(v, Version) <= 0 {
		return ""
	}
	return v
}

// SelfUpdate replaces this binary with the latest release (checksum
// verified). It returns the installed version, or "" when already current.
func SelfUpdate(ctx context.Context) (string, error) {
	if Pinned {
		return "", errors.New("this coolify-mirror build is pinned by kombify StackKits; it is updated only with a StackKits release")
	}
	latest, err := LatestRelease(ctx)
	if err != nil {
		return "", fmt.Errorf("could not reach GitHub: %w", err)
	}
	if CompareVersions(latest, Version) <= 0 {
		return "", nil
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return "", err
	}
	name := "coolify-mirror-linux-" + runtime.GOARCH
	base := RepoURL + "/releases/download/v" + latest + "/"
	sums, err := download(ctx, base+"SHA256SUMS", 1<<16)
	if err != nil {
		return "", err
	}
	want := ""
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	if want == "" {
		return "", fmt.Errorf("release %s has no %s", latest, name)
	}
	bin, err := download(ctx, base+name, 200<<20)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bin)
	if hex.EncodeToString(sum[:]) != want {
		return "", errors.New("checksum mismatch - not updated")
	}
	tmp := self + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, self); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return latest, nil
}

func download(ctx context.Context, url string, max int64) ([]byte, error) {
	var last error
	for try := 0; try < 4; try++ {
		if try > 0 {
			time.Sleep(3 * time.Second)
		}
		rctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		req, _ := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			last = err
			continue
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, max))
		resp.Body.Close()
		cancel()
		if err == nil && resp.StatusCode == http.StatusOK {
			return b, nil
		}
		last = fmt.Errorf("%s: HTTP %s %v", url, resp.Status, err)
	}
	return nil, last
}
