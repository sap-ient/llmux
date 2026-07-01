package server

import (
	"log"
	"net/http"

	"github.com/llmux/llmux/core/openai"
	"github.com/llmux/llmux/core/provider"
	"github.com/llmux/llmux/core/sovereign"
)

// enforceSovereignty is the dispatch-time gate that makes "nothing leaves the
// box unless you say so" a real, enforced property. It returns nil when a
// request may be sent to provName, or a 403 provider.Error when provName is a
// non-local endpoint the operator has not explicitly opted in (allow_egress).
//
// The check happens BEFORE any network call, so a denied provider never even
// opens a connection. Permitted egress is logged/labeled on every request so
// off-box traffic is always observable, never silent.
func (s *Server) enforceSovereignty(provName string) error {
	d := s.sovereign.Check(provName)
	if !d.Allowed {
		s.metrics.incEgressBlocked()
		s.log.Warn("sovereignty: blocked egress",
			"provider", provName, "locality", string(d.Locality), "base_url", d.BaseURL)
		return &provider.Error{
			StatusCode: http.StatusForbidden,
			Provider:   provName,
			Body: openai.NewError(
				"sovereignty: provider \""+provName+"\" is a non-local endpoint and egress is not enabled; "+
					"set \"allow_egress\": true on this provider to permit off-box calls",
				"sovereignty_error", "egress_not_allowed"),
		}
	}
	if d.Locality == sovereign.Egress {
		// Allowed, but observable: label every off-box call.
		s.log.Info("sovereignty: egress permitted (operator opt-in)",
			"provider", provName, "base_url", d.BaseURL)
	}
	return nil
}

// logSovereignty prints the sovereignty posture at startup so operators can see
// exactly which providers are on-box and which are permitted to leave it.
func logSovereignty(p *sovereign.Policy) {
	var local, egressOK, egressBlocked []string
	for _, d := range p.Decisions() {
		switch {
		case d.Locality == sovereign.Local:
			local = append(local, d.Provider)
		case d.Allowed:
			egressOK = append(egressOK, d.Provider)
		default:
			egressBlocked = append(egressBlocked, d.Provider)
		}
	}
	log.Printf("llmux sovereignty: local(on-box)=%v egress-allowed=%v egress-blocked=%v",
		local, egressOK, egressBlocked)
	if len(egressBlocked) > 0 {
		log.Printf("llmux sovereignty: %d provider(s) are non-local and BLOCKED by default; "+
			"set \"allow_egress\": true on a provider to permit off-box calls", len(egressBlocked))
	}
}
