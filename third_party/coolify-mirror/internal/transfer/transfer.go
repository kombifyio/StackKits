// Package transfer shares a backup file over HTTPS (pinned self-signed
// certificate, see tls.go) and downloads it on the other server. The file is
// also encrypted itself; the decryption key and the certificate pin travel only
// in the URL fragment (#key=...&pin=...), which clients never send.
package transfer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// BackupName is the file name used in share links.
const BackupName = "backup.cmb"

// ToolName is the path the tool binary itself is served under.
const ToolName = "coolify-mirror"

// NewToken returns a random URL token.
func NewToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ParseSource splits a pasted share code, link or file path into location
// and key. Accepted: HOST[:PORT]/CODE (the key is then fetched with FetchKey),
// https://…/backup.cmb#key=KEY&pin=PIN, /path/file.cmb#key=KEY, /path/file.cmb. For links the returned location keeps "#pin=PIN" (the
// download needs it; it is never sent). Plain http links are refused.
func ParseSource(s string) (location, key string, isURL bool, err error) {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	if s == "" {
		return "", "", false, errors.New("empty link")
	}
	if loc, secret, ok, err := parseCode(s); ok {
		if err != nil {
			return "", "", false, err
		}
		return loc, codeKeyPrefix + secret, true, nil
	}
	loc, frag, _ := strings.Cut(s, "#")
	pin := ""
	if frag != "" {
		if !strings.Contains(frag, "=") {
			key = frag
		} else if q, err := url.ParseQuery(frag); err == nil {
			key, pin = q.Get("key"), q.Get("pin")
		}
	}
	key = strings.TrimSpace(key)
	if strings.HasPrefix(loc, "http://") {
		return "", "", false, errors.New("this is an unencrypted http:// link - links are https only; share the backup again with this version of coolify-mirror on the source server")
	}
	if strings.HasPrefix(loc, "https://") {
		u, err := url.Parse(loc)
		if err != nil {
			return "", "", false, fmt.Errorf("invalid link: %w", err)
		}
		if pin == "" {
			return "", "", false, errors.New("the link has no certificate pin (#...&pin=) - copy the whole link")
		}
		if _, err := decodePin(pin); err != nil {
			return "", "", false, err
		}
		if tokenFromURL(u) == "" {
			return "", "", false, errors.New("invalid link: no share token in it")
		}
		return loc + "#pin=" + url.QueryEscape(pin), key, true, nil
	}
	return loc, key, false, nil
}

// --- server --------------------------------------------------------------------

// Event is a transfer progress event emitted by the server.
type Event struct {
	Remote   string `json:"remote"`
	Sent     int64  `json:"sent"`
	Total    int64  `json:"total"`
	Complete bool   `json:"complete"`
	Path     string `json:"path"`
}

// Server shares one backup file (and the tool binary) under a secret token.
type Server struct {
	File    string
	Token   string
	Binary  string // path of this executable, served as /cm/<token>/coolify-mirror
	Cert    *Cert  // TLS certificate (required by Listen)
	KeyBlob []byte // the backup key encrypted with a share code (WrapKey), served as /cm/<token>/key
	OnEvent func(Event)

	completed atomic.Int64
	srv       *http.Server
	mu        sync.Mutex
}

// Completed is the number of finished full downloads of the backup.
func (s *Server) Completed() int64 { return s.completed.Load() }

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	base := "/cm/" + s.Token + "/"
	mux.HandleFunc(base+BackupName, func(w http.ResponseWriter, r *http.Request) {
		s.serveFile(w, r, s.File, true)
	})
	mux.HandleFunc(base+ToolName, func(w http.ResponseWriter, r *http.Request) {
		if s.Binary == "" {
			http.NotFound(w, r)
			return
		}
		s.serveFile(w, r, s.Binary, false)
	})
	mux.HandleFunc(base+"key", func(w http.ResponseWriter, r *http.Request) {
		if len(s.KeyBlob) == 0 || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(s.KeyBlob)
	})
	mux.HandleFunc(base+"info", func(w http.ResponseWriter, r *http.Request) {
		st, err := os.Stat(s.File)
		if err != nil {
			http.Error(w, "gone", http.StatusGone)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": filepath.Base(s.File), "size": st.Size(), "modified": st.ModTime().UTC()})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	return mux
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, path string, track bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "gone", http.StatusGone)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "gone", http.StatusGone)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	cr := &countingSeeker{f: f, size: st.Size()}
	if track && s.OnEvent != nil && r.Method == http.MethodGet {
		stop := make(chan struct{})
		go func() {
			t := time.NewTicker(500 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					s.OnEvent(Event{Remote: r.RemoteAddr, Sent: cr.pos.Load(), Total: st.Size(), Path: r.URL.Path})
				}
			}
		}()
		defer func() {
			close(stop)
			done := cr.pos.Load() >= st.Size()
			if done {
				s.completed.Add(1)
			}
			s.OnEvent(Event{Remote: r.RemoteAddr, Sent: cr.pos.Load(), Total: st.Size(), Complete: done, Path: r.URL.Path})
		}()
	}
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), cr)
}

// countingSeeker records the furthest position read.
type countingSeeker struct {
	f    *os.File
	size int64
	pos  atomic.Int64
	cur  int64
}

func (c *countingSeeker) Read(p []byte) (int, error) {
	n, err := c.f.Read(p)
	c.cur += int64(n)
	if c.cur > c.pos.Load() {
		c.pos.Store(c.cur)
	}
	return n, err
}

func (c *countingSeeker) Seek(off int64, whence int) (int64, error) {
	n, err := c.f.Seek(off, whence)
	if err == nil {
		c.cur = n
	}
	return n, err
}

// Listen starts serving HTTPS on addr (":8123"). It returns the bound address.
func (s *Server) Listen(addr string) (net.Addr, error) {
	if s.Cert == nil {
		return nil, errors.New("sharing needs a TLS certificate")
	}
	tcp, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	ln := tls.NewListener(tcp, s.Cert.serverConfig())
	s.mu.Lock()
	s.srv = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 30 * time.Second}
	srv := s.srv
	s.mu.Unlock()
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr(), nil
}

// Close stops the server.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.srv.Shutdown(ctx); err != nil {
			// Active downloads do not end by themselves: cut them off.
			_ = s.srv.Close()
		}
		s.srv = nil
	}
}

// --- client --------------------------------------------------------------------

// Info is what /info returns.
type Info struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// newClient returns the HTTPS client for a link (with "#pin=..." from
// ParseSource) and the URL to request (fragment removed).
func newClient(link string) (*http.Client, string, error) {
	loc, frag, _ := strings.Cut(link, "#")
	u, err := url.Parse(loc)
	if err != nil {
		return nil, "", err
	}
	if u.Scheme != "https" {
		return nil, "", errors.New("only https links are accepted")
	}
	q, _ := url.ParseQuery(frag)
	cfg, err := pinnedConfig(q.Get("pin"), SNIName(tokenFromURL(u)))
	if err != nil {
		return nil, "", err
	}
	return &http.Client{
		// Never follow redirects away from the pinned server.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ResponseHeaderTimeout: 60 * time.Second,
			TLSClientConfig:       cfg,
			ForceAttemptHTTP2:     true,
		}}, loc, nil
}

// RemoteSize asks the server for the size of the shared file (HEAD request).
func RemoteSize(ctx context.Context, link string) (int64, error) {
	size, _, err := RemoteInfo(ctx, link)
	return size, err
}

// RemoteInfo returns the size and file name of the shared backup (kombify
// fork addition): the name tells one backup behind a share link from another.
func RemoteInfo(ctx context.Context, link string) (int64, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client, loc, err := newClient(link)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, loc, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", explainNetErr(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return 0, "", fmt.Errorf("the backup is not shared at this link anymore (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("HTTP %s", resp.Status)
	}
	name := ""
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		name = filepath.Base(params["filename"])
	}
	return resp.ContentLength, name, nil
}

// Download fetches link into dest, resuming a previous partial download
// (dest+".part") and retrying on network errors. progress gets (done, total).
func Download(ctx context.Context, link, dest string, progress func(done, total int64)) error {
	part := dest + ".part"
	client, link, err := newClient(link)
	if err != nil {
		return err
	}
	var total int64 = -1
	var lastErr error
	connected := false // got at least one HTTP response
	for attempt := 0; attempt < 12; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A server that was never reachable is most likely firewalled or not
		// sharing at all: give up after a few quick tries instead of minutes.
		if !connected && attempt >= 3 {
			break
		}
		if attempt > 0 {
			wait := time.Duration(attempt*attempt) * time.Second
			if wait > 30*time.Second {
				wait = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		var have int64
		if st, err := os.Stat(part); err == nil {
			have = st.Size()
		}
		if total >= 0 && have == total {
			return os.Rename(part, dest)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
		if err != nil {
			return err
		}
		if have > 0 {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = explainNetErr(err)
			continue
		}
		connected = true
		switch {
		case resp.StatusCode == http.StatusPartialContent:
			if cr := resp.Header.Get("Content-Range"); cr != "" {
				if i := strings.LastIndexByte(cr, '/'); i >= 0 {
					total, _ = strconv.ParseInt(cr[i+1:], 10, 64)
				}
			}
		case resp.StatusCode == http.StatusOK:
			have = 0
			total = resp.ContentLength
		case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
			resp.Body.Close()
			_ = os.Remove(part)
			lastErr = errors.New("server rejected resume; restarting download")
			continue
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
			resp.Body.Close()
			return fmt.Errorf("the backup is not shared at this link anymore (HTTP %d) - start sharing again on the source server", resp.StatusCode)
		default:
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %s", resp.Status)
			continue
		}
		flags := os.O_CREATE | os.O_WRONLY
		if have > 0 {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		f, err := os.OpenFile(part, flags, 0o600)
		if err != nil {
			resp.Body.Close()
			return err
		}
		pw := &progressWriter{w: f, done: have, total: total, fn: progress}
		if progress != nil {
			progress(have, total)
		}
		_, err = io.CopyBuffer(pw, resp.Body, make([]byte, 1<<20))
		resp.Body.Close()
		cerr := f.Close()
		if err == nil {
			err = cerr
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = explainNetErr(err)
			continue
		}
		if st, err := os.Stat(part); err == nil && (total < 0 || st.Size() == total) {
			return os.Rename(part, dest)
		}
		lastErr = errors.New("connection closed before the download finished")
	}
	if lastErr == nil {
		lastErr = errors.New("download failed")
	}
	return lastErr
}

type progressWriter struct {
	w           io.Writer
	done, total int64
	fn          func(int64, int64)
	last        time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.fn != nil && time.Since(p.last) > 200*time.Millisecond {
		p.last = time.Now()
		p.fn(p.done, p.total)
	}
	return n, err
}

func explainNetErr(err error) error {
	var ne net.Error
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("%w - is the source still sharing, and is its port open in the firewall?", err)
	case errors.As(err, &ne) && ne.Timeout():
		return fmt.Errorf("%w - the source server's port is probably blocked by a firewall (try the port-80 sharing mode)", err)
	}
	return err
}
