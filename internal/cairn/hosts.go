package cairn

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// Host allow-lists. A browser sends the name it looked up in the Host
// header, so a hostile website that points its own name at the private
// address (DNS rebinding) arrives with its own name there. Each listener
// answers only the names its owner reaches it by and refuses the rest before
// any handler runs. Form Origin checks use the same list.

// hostPolicy is one listener's list of acceptable Host names.
type hostPolicy struct {
	// names are exact host names, lower case, without a port.
	names map[string]bool
	// private also accepts any IP literal, single-label names (tailnet
	// short names such as "raspberrypi") and names under .ts.net (MagicDNS
	// and tailscale serve). The public listener accepts only loopback IPs,
	// localhost and its named hosts.
	private bool
}

type hostPolicyKey struct{}

// newHostPolicy builds a listener's list from its base URLs and extra names.
func newHostPolicy(private bool, baseURLs []string, extra []string) *hostPolicy {
	p := &hostPolicy{names: map[string]bool{}, private: private}
	for _, base := range baseURLs {
		if u, err := url.Parse(base); err == nil && u.Host != "" {
			if host, ok := hostName(u.Host); ok {
				p.names[host] = true
			}
		}
	}
	for _, name := range extra {
		if host, ok := hostName(name); ok {
			p.names[host] = true
		}
	}
	return p
}

// allows reports whether a Host header (name or IP, with or without a port)
// is one this listener answers.
func (p *hostPolicy) allows(hostport string) bool {
	host, ok := hostName(hostport)
	if !ok {
		return false
	}
	if p.names[host] || host == "localhost" {
		return true
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return p.private || ip.Unmap().IsLoopback()
	}
	if !p.private {
		return false
	}
	return !strings.Contains(host, ".") || strings.HasSuffix(host, ".ts.net")
}

// hostName reads the host from a Host header or a name in a setting: it
// drops the port, IPv6 brackets and a trailing dot and folds case. ok is
// false for anything that is not a well-formed name or IP address.
func hostName(hostport string) (string, bool) {
	host, port := hostport, ""
	if strings.HasPrefix(host, "[") {
		end := strings.IndexByte(host, ']')
		if end < 0 {
			return "", false
		}
		host, port = host[1:end], host[end+1:]
		if _, err := netip.ParseAddr(host); err != nil {
			return "", false
		}
	} else if i := strings.LastIndexByte(host, ':'); i >= 0 {
		if strings.Count(host, ":") > 1 {
			// An IPv6 literal must be bracketed in a Host header.
			return "", false
		}
		host, port = host[:i], host[i:]
	}
	if port != "" {
		digits := strings.TrimPrefix(port, ":")
		if digits == port || digits == "" || len(digits) > 5 || strings.Trim(digits, "0123456789") != "" {
			return "", false
		}
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return "", false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return host, true
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") || strings.Trim(label, "abcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
			return "", false
		}
	}
	return host, len(host) <= 253
}

// ValidHostName reports whether name is a host name or IP address that can
// go in Config.AllowedHosts. A port is allowed and ignored.
func ValidHostName(name string) bool {
	_, ok := hostName(strings.TrimSpace(name))
	return ok
}

// checkAllowedHosts validates the extra names a server was given.
func checkAllowedHosts(names []string) error {
	for _, name := range names {
		if _, ok := hostName(strings.TrimSpace(name)); !ok {
			return fmt.Errorf("allowed host %q is not a host name", name)
		}
	}
	return nil
}

// withHostPolicy records the listener's list on the request, so Origin
// checks deeper in the handlers compare against the same names.
func withHostPolicy(r *http.Request, p *hostPolicy) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), hostPolicyKey{}, p))
}

// refuseHost answers a request for a name this listener does not serve,
// and logs the name so the owner can find it.
func (s *Server) refuseHost(w http.ResponseWriter, r *http.Request, visitor bool) {
	noteRequest(r, "host refused", slog.String("host", clip(r.Host, 100)))
	w.Header().Set("Cache-Control", "no-store")
	if visitor {
		s.showVisitorError(w, http.StatusMisdirectedRequest, problemBrokenLink)
		return
	}
	host, _ := hostName(r.Host)
	if host == "" {
		host = "that name"
	}
	s.showError(w, http.StatusMisdirectedRequest, problem{
		Title:   "This address isn’t allowed",
		Message: "Cairn answers only at addresses you have told it about, so other websites can’t reach your library. To open Cairn at " + host + ", add " + host + " to CAIRN_ALLOWED_HOSTS (or -allowed-hosts) on the server, or set CAIRN_PRIVATE_BASE_URL to this address, then restart Cairn.",
	})
}
