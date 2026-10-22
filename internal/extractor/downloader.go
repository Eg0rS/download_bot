package extractor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ValidateURL rejects local services and non-HTTP schemes before dispatch.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return fmt.Errorf("invalid HTTP(S) URL")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("local addresses are not allowed")
	}
	if ip, err := netip.ParseAddr(host); err == nil && !publicIP(ip) {
		return fmt.Errorf("private addresses are not allowed")
	}
	return nil
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!netip.MustParsePrefix("100.64.0.0/10").Contains(ip) &&
		!netip.MustParsePrefix("169.254.0.0/16").Contains(ip)
}

// Resolve and check on every connection, including redirects, to resist DNS rebinding.
func safeHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Minute,
		Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if !publicIP(ip) {
					return nil, fmt.Errorf("private addresses are not allowed")
				}
			}
			var last error
			for _, ip := range ips {
				conn, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			if last == nil {
				last = fmt.Errorf("no addresses found")
			}
			return nil, last
		}},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return ValidateURL(req.URL.String())
		},
	}
}

type HTTPDownloader struct {
	client   *http.Client
	tempDir  string
	maxBytes int64
}

func maxDownloadBytes() int64 {
	n, _ := strconv.ParseInt(os.Getenv("MAX_FILE_SIZE_MB"), 10, 64)
	if n <= 0 || n > 50 {
		n = 50
	}
	return n * 1024 * 1024
}

func NewHTTPDownloader(tempDir string) *HTTPDownloader {
	return &HTTPDownloader{client: safeHTTPClient(), tempDir: tempDir, maxBytes: maxDownloadBytes()}
}

func tempDir() string {
	if dir := os.Getenv("TEMP_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "media-downloader")
}

func (d *HTTPDownloader) Download(ctx context.Context, rawURL, dest string, progress ProgressFunc) (err error) {
	if err = ValidateURL(rawURL); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > d.maxBytes {
		return fmt.Errorf("file exceeds size limit")
	}
	if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(dest)
		}
	}()
	n, err := io.Copy(f, io.LimitReader(resp.Body, d.maxBytes+1))
	if err != nil {
		return err
	}
	if n > d.maxBytes {
		return fmt.Errorf("file exceeds size limit")
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return io.ErrUnexpectedEOF
	}
	if progress != nil {
		progress(n, n, 0, 0)
	}
	return nil
}

func (d *HTTPDownloader) GetFileSize(rawURL string) (int64, error) {
	if err := ValidateURL(rawURL); err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.ContentLength, nil
}

func (d *HTTPDownloader) GetTempFile(ext string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	ext = strings.TrimPrefix(filepath.Ext("file."+ext), ".")
	return filepath.Join(d.tempDir, "dl_"+hex.EncodeToString(b[:])+"."+ext)
}

func (d *HTTPDownloader) Cleanup(paths ...string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}
