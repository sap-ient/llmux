// Package sovereign enforces llmux's sovereignty guarantee: by default, a
// request may only reach a model that runs on THIS box (a loopback / unix-socket
// backend). Any endpoint that would send data off the box (an "egress" provider)
// is DENIED unless the operator has explicitly opted that provider in. Nothing
// leaves the box unless you say so — and when it does, it is logged/labeled so
// egress is always observable, never silent.
//
// This package is dependency-free aside from core/config and speaks only about
// provider base URLs, so it can be reused by the server and by tooling.
package sovereign

import (
	"net"
	"net/url"
	"strings"

	"github.com/llmux/llmux/core/config"
)

// Locality classifies where a provider's traffic goes.
type Locality string

const (
	// Local means the backend runs on THIS box: a loopback address
	// (localhost, 127.0.0.0/8, ::1) or a unix socket. Data never leaves.
	Local Locality = "local"
	// Egress means calling the provider sends the request off the box to a
	// remote endpoint. Permitted only with explicit operator opt-in.
	Egress Locality = "egress"
)

// LocalityOf classifies a provider base URL. Only a loopback host (localhost,
// 127.0.0.0/8, ::1) or a unix-socket / on-box filesystem-path target is Local;
// everything else — public hosts, private LAN addresses, docker service names —
// is Egress. It fails CLOSED: an EMPTY base URL (e.g. an adapter like Bedrock
// that reaches a cloud region without a base URL) and any unparseable/hostless
// URL are treated as Egress, so a provider is never assumed on-box by accident.
func LocalityOf(baseURL string) Locality {
	s := strings.TrimSpace(baseURL)
	if s == "" {
		// No on-box endpoint we can point to. Fail closed: cloud adapters with
		// no base URL (Bedrock) must not be mistaken for a local server.
		return Egress
	}
	// unix:///path or a bare filesystem path => on-box socket.
	if strings.HasPrefix(s, "unix:") || strings.HasPrefix(s, "/") {
		return Local
	}
	host := hostOf(s)
	if host == "" {
		return Egress // unparseable / hostless: fail closed
	}
	if isLoopbackHost(host) {
		return Local
	}
	return Egress
}

// hostOf extracts the hostname from a base URL, or "" if none can be parsed.
func hostOf(baseURL string) string {
	s := baseURL
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// isLoopbackHost reports whether host names this machine's loopback interface.
func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// Decision is the sovereignty verdict for one provider.
type Decision struct {
	Provider string
	BaseURL  string
	Locality Locality
	// Allowed reports whether a request may be dispatched to this provider.
	// Local providers are always allowed; Egress providers only when the
	// operator set allow_egress on them.
	Allowed bool
}

// Policy classifies the configured providers and enforces the local-default
// guarantee. It is built once from config and consulted before every dispatch.
type Policy struct {
	byName map[string]Decision
}

// NewPolicy builds a Policy from provider configs. A provider is Allowed iff it
// is Local, or it is Egress AND the operator explicitly set allow_egress.
func NewPolicy(cfgs []config.ProviderConfig) *Policy {
	m := make(map[string]Decision, len(cfgs))
	for _, c := range cfgs {
		loc := LocalityOf(c.BaseURL)
		m[c.Name] = Decision{
			Provider: c.Name,
			BaseURL:  c.BaseURL,
			Locality: loc,
			Allowed:  loc == Local || c.AllowEgress,
		}
	}
	return &Policy{byName: m}
}

// Check returns the sovereignty decision for a provider name. An unknown
// provider fails CLOSED: it is reported as a denied egress target.
func (p *Policy) Check(provider string) Decision {
	if d, ok := p.byName[provider]; ok {
		return d
	}
	return Decision{Provider: provider, Locality: Egress, Allowed: false}
}

// AllowedEgress returns the names of providers explicitly opted in to leave the
// box, so startup/health can label exactly what may egress.
func (p *Policy) AllowedEgress() []string {
	var out []string
	for name, d := range p.byName {
		if d.Locality == Egress && d.Allowed {
			out = append(out, name)
		}
	}
	return out
}

// Decisions returns a snapshot of every provider's decision (for disclosure).
func (p *Policy) Decisions() []Decision {
	out := make([]Decision, 0, len(p.byName))
	for _, d := range p.byName {
		out = append(out, d)
	}
	return out
}
