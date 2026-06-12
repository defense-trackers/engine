package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sensitiveQueryKeys are query parameters whose values must never appear in an
// error message, log line, or status.json. The engine injects secrets only as
// headers (never query strings), so this is belt-and-suspenders against a future
// contract or upstream redirect that puts a key in a URL.
var sensitiveQueryKeys = map[string]bool{
	"api_key": true, "apikey": true, "key": true, "token": true,
	"access_token": true, "auth": true, "password": true, "secret": true,
}

// redactURL replaces the values of any sensitive query parameters with "REDACTED"
// so a URL can be safely embedded in a user-visible error. Falls back to the raw
// string only if it doesn't parse (and never contains a secret in that case,
// since we don't build query-auth URLs).
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	changed := false
	for k := range q {
		if sensitiveQueryKeys[strings.ToLower(k)] {
			q.Set(k, "REDACTED")
			changed = true
		}
	}
	if !changed {
		return raw
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// UserAgent identifies the engine to source operators. TODO: point the URL
// at the public repo once created — operators should be able to find you.
const UserAgent = "defense-trackers/0.1 (+https://github.com/defense-trackers)"

const maxBody = 20 << 20 // 20 MiB cap per fetch

type cacheMeta struct {
	ETag         string `json:"etag"`
	LastModified string `json:"last_modified"`
}

func cacheKey(url string) string {
	h := sha256.Sum256([]byte(url))
	return hex.EncodeToString(h[:8])
}

// BodyCachePath returns where the last fetched body for url is stored.
// The quarantine step copies this into the repair bundle.
func BodyCachePath(cacheDir, url string) string {
	return filepath.Join(cacheDir, cacheKey(url)+".body")
}

func metaCachePath(cacheDir, url string) string {
	return filepath.Join(cacheDir, cacheKey(url)+".meta")
}

// FetchURL performs a conditional GET with retries and exponential backoff.
// 200 stores etag/last-modified + body and returns the body; 304 returns the
// cached body; 5xx and transport errors retry; other statuses fail fast.
func FetchURL(url, cacheDir string) ([]byte, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	var meta cacheMeta
	if b, err := os.ReadFile(metaCachePath(cacheDir, url)); err == nil {
		_ = json.Unmarshal(b, &meta)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<attempt)*time.Second +
				time.Duration(rand.Intn(500))*time.Millisecond
			time.Sleep(backoff)
		}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", UserAgent)
		if meta.ETag != "" {
			req.Header.Set("If-None-Match", meta.ETag)
		}
		if meta.LastModified != "" {
			req.Header.Set("If-Modified-Since", meta.LastModified)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusNotModified:
			cached, err := os.ReadFile(BodyCachePath(cacheDir, url))
			if err != nil {
				// Meta survived but body didn't; clear meta and retry fresh.
				_ = os.Remove(metaCachePath(cacheDir, url))
				meta = cacheMeta{}
				lastErr = fmt.Errorf("304 with empty cache for %s", redactURL(url))
				continue
			}
			return cached, nil
		case resp.StatusCode == http.StatusOK:
			if readErr != nil {
				lastErr = readErr
				continue
			}
			meta = cacheMeta{
				ETag:         resp.Header.Get("ETag"),
				LastModified: resp.Header.Get("Last-Modified"),
			}
			mb, _ := json.Marshal(meta)
			_ = os.WriteFile(metaCachePath(cacheDir, url), mb, 0o644)
			_ = os.WriteFile(BodyCachePath(cacheDir, url), body, 0o644)
			return body, nil
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("http %d from %s", resp.StatusCode, redactURL(url))
			continue
		default:
			return nil, fmt.Errorf("http %d from %s", resp.StatusCode, redactURL(url))
		}
	}
	return nil, fmt.Errorf("fetch %s: %w", redactURL(url), lastErr)
}

// FetchRaw performs an arbitrary-method request with custom headers and an
// optional body, retrying transport errors and 5xx with backoff. No etag
// cache (POST and most JSON APIs don't honor 304). Used by the api fetcher.
func FetchRaw(method, url string, headers map[string]string, body []byte) ([]byte, error) {
	client := &http.Client{Timeout: 45 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<attempt)*time.Second +
				time.Duration(rand.Intn(500))*time.Millisecond
			time.Sleep(backoff)
		}
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, url, rdr)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("Accept", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		respHeader := resp.Header
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
			if readErr != nil {
				lastErr = readErr
				continue
			}
			lastHeader = respHeader // expose Link header to the api fetcher
			return b, nil
		case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
			lastErr = fmt.Errorf("http %d from %s", resp.StatusCode, redactURL(url))
			continue
		default:
			return nil, fmt.Errorf("http %d from %s", resp.StatusCode, redactURL(url))
		}
	}
	return nil, fmt.Errorf("fetch %s: %w", redactURL(url), lastErr)
}

// lastHeader carries the most recent response headers (e.g. Link) to the api
// fetcher's pagination logic. Single-threaded engine, so this is safe.
var lastHeader http.Header

// LastLinkHeader returns the Link header from the most recent FetchRaw.
func LastLinkHeader() string { return lastHeader.Get("Link") }
