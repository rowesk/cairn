package cairn

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/html"
)

var errWebAddress = errors.New("refused address")

// Tests may replace this guard to serve fixtures locally. Control receives
// the numeric address selected by Dialer, never a hostname checked earlier.
var webImageAddressAllowed = publicImageAddress

// Tests also allow the fixture server's ephemeral port at the URL gate.
var webImageURLAllowed = checkWebImageURL

func publicImageAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || (port != "80" && port != "443") {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return ip.Is4() || netip.MustParsePrefix("2000::/3").Contains(ip)
}

func webImageURL(src string) bool {
	u, err := url.Parse(src)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https" || (u.Scheme == "" && strings.HasPrefix(src, "//")))
}

func webImagePrefix(src string) string { return fmt.Sprintf("web-%x", sha256.Sum256([]byte(src)))[:20] }

func imageExtension(data []byte) string {
	switch http.DetectContentType(data) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	return ""
}

func checkWebImageURL(u *url.URL) error {
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Port() != "" && u.Port() != "80" && u.Port() != "443") {
		return errWebAddress
	}
	return nil
}

func fetchWebImage(ctx context.Context, client *http.Client, src string) ([]byte, string) {
	if strings.HasPrefix(src, "//") {
		src = "https:" + src
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil || webImageURLAllowed(req.URL) != nil {
		return nil, "refused address"
	}
	req.Header.Set("User-Agent", "Cairn")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, errWebAddress) {
			return nil, "refused address"
		}
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return nil, "timed out"
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil, "timed out"
		}
		return nil, "fetch failed"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes+1))
	if len(data) > maxAssetBytes {
		return nil, "too large"
	}
	if err != nil {
		var timeout net.Error
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
			return nil, "timed out"
		}
		return nil, "fetch failed"
	}
	if imageExtension(data) == "" {
		return nil, "not an image"
	}
	return data, ""
}

// Fetch from the validated HTML, so code blocks and removed HTML never cause
// requests. Keep the source URLs as keys, including protocol-relative spelling.
func (s *Server) fetchWebImages(ctx context.Context, r *http.Request, in *publication) []apiWarning {
	root, _ := parseBody(in.HTML)
	urls := map[string]string{}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" && webImageURL(attr(n, "src")) {
			urls[attr(n, "src")] = ""
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(root)
	if len(urls) == 0 {
		return nil
	}
	names := make([]string, 0, len(urls))
	for src := range urls {
		names = append(names, src)
	}
	sort.Strings(names)
	if in.Assets == nil {
		in.Assets = map[string][]byte{}
	}
	// Read only this author's previous object under the read lock. Do not hold
	// the lock during any network operation. Replacement still rechecks ownership.
	if r.Method == http.MethodPut {
		slug := strings.TrimPrefix(r.URL.Path, "/api/pages/")
		s.mu.RLock()
		p, err := s.getPage(slug)
		cred := credential{}
		if c := publishContextOf(r); c != nil {
			cred = c.cred
		}
		if err == nil && cred.reaches(p.Agent) {
			for _, src := range names {
				for _, ext := range []string{".png", ".jpg", ".gif", ".webp"} {
					name := webImagePrefix(src) + ext
					if _, exists := in.Assets[name]; !exists && len(in.Assets) < maxAssets {
						if data, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "objects", p.Object, "assets", name)); err == nil && len(data) <= maxAssetBytes && imageExtension(data) == ext {
							in.Assets[name] = data
						}
					}
				}
			}
		}
		s.mu.RUnlock()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		if !webImageAddressAllowed(address) {
			return errWebAddress
		}
		return nil
	}}
	transport := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return errors.New("too many redirects")
		}
		if err := webImageURLAllowed(req.URL); err != nil {
			return err
		}
		req.Header.Set("User-Agent", "Cairn")
		return nil
	}}
	type result struct {
		src    string
		data   []byte
		reason string
	}
	results := make(chan result, len(names))
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	reserved := len(in.Assets)
	for _, src := range names {
		for name, data := range in.Assets {
			if strings.TrimSuffix(name, filepath.Ext(name)) == webImagePrefix(src) && imageExtension(data) == filepath.Ext(name) {
				urls[src] = name
				break
			}
		}
		if urls[src] != "" {
			continue
		}
		if reserved >= maxAssets {
			results <- result{src: src, reason: "32-asset limit"}
			continue
		}
		reserved++
		wg.Add(1)
		go func(src string) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				results <- result{src: src, reason: "timed out"}
				return
			}
			data, reason := fetchWebImage(ctx, client, src)
			results <- result{src, data, reason}
		}(src)
	}
	wg.Wait()
	close(results)
	var failures []string
	for res := range results {
		if res.reason != "" {
			failures = append(failures, res.src+": "+res.reason)
			continue
		}
		name := webImagePrefix(res.src) + imageExtension(res.data)
		in.Assets[name] = res.data
		urls[res.src] = name
	}
	in.WebImages = urls
	sort.Strings(failures)
	if len(failures) == 0 {
		return nil
	}
	return []apiWarning{{Code: "web_image_failed", Message: "Cairn could not save these web images: " + strings.Join(failures, "; ") + "."}}
}

// Run before figures and full-size links are built, so they use the same asset.
func mapWebImages(n *html.Node, images map[string]string) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && c.Data == "img" {
			src := attr(c, "src")
			var link *html.Node
			for parent := n; parent != nil; parent = parent.Parent {
				if parent.Type == html.ElementNode && parent.Data == "a" {
					link = parent
					break
				}
			}
			if name, ok := images[src]; ok {
				if name != "" {
					if link != nil && attr(link, "href") == src {
						for i := range link.Attr {
							if link.Attr[i].Key == "href" {
								link.Attr[i].Val = "?asset=" + name
							}
						}
					}
					for i := range c.Attr {
						if c.Attr[i].Key == "src" {
							c.Attr[i].Val = "?asset=" + name
						}
					}
				} else {
					a := element("a", "href", src)
					a.AppendChild(&html.Node{Type: html.TextNode, Data: attr(c, "alt")})
					if link != nil {
						n.InsertBefore(&html.Node{Type: html.TextNode, Data: attr(c, "alt")}, c)
						for i := range link.Attr {
							if link.Attr[i].Key == "href" {
								link.Attr[i].Val = src
							}
						}
					} else {
						n.InsertBefore(a, c)
					}
					n.RemoveChild(c)
				}
			}
		} else {
			mapWebImages(c, images)
		}
		c = next
	}
}
