package sovereign

import (
	"testing"

	"github.com/llmux/llmux/core/config"
)

func TestLocalityOf(t *testing.T) {
	cases := []struct {
		url  string
		want Locality
	}{
		// On-box: loopback + unix socket + empty.
		{"http://localhost:11434/v1", Local},
		{"http://127.0.0.1:8080/v1", Local},
		{"https://127.0.0.1/v1", Local},
		{"http://[::1]:11434/v1", Local},
		{"http://LOCALHOST:11434/v1", Local},
		{"http://foo.localhost:11434/v1", Local},
		{"unix:///run/llmux.sock", Local},
		{"/run/ollama.sock", Local},
		// Empty base URL fails closed to Egress (e.g. Bedrock reaches AWS with
		// no base URL and must never be mistaken for an on-box server).
		{"", Egress},
		// Off-box: public + private LAN + docker service names all egress.
		{"https://api.openai.com/v1", Egress},
		{"https://api.anthropic.com/v1", Egress},
		{"http://192.168.1.50:11434/v1", Egress},
		{"http://10.0.0.5:11434/v1", Egress},
		{"http://ollama:11434/v1", Egress},
		{"http://example.com", Egress},
	}
	for _, c := range cases {
		if got := LocalityOf(c.url); got != c.want {
			t.Errorf("LocalityOf(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestPolicyDefaultDeniesEgress(t *testing.T) {
	p := NewPolicy([]config.ProviderConfig{
		{Name: "local", BaseURL: "http://localhost:11434/v1"},
		{Name: "openai", BaseURL: "https://api.openai.com/v1"},                        // not opted in
		{Name: "broker", BaseURL: "https://broker.example.com/v1", AllowEgress: true}, // opted in
	})

	if d := p.Check("local"); !d.Allowed || d.Locality != Local {
		t.Errorf("local: got %+v, want allowed local", d)
	}
	if d := p.Check("openai"); d.Allowed || d.Locality != Egress {
		t.Errorf("openai (no opt-in) must be a DENIED egress; got %+v", d)
	}
	if d := p.Check("broker"); !d.Allowed || d.Locality != Egress {
		t.Errorf("broker (opted in) must be an ALLOWED egress; got %+v", d)
	}
	// Unknown providers fail closed.
	if d := p.Check("ghost"); d.Allowed {
		t.Errorf("unknown provider must fail closed; got %+v", d)
	}

	eg := p.AllowedEgress()
	if len(eg) != 1 || eg[0] != "broker" {
		t.Errorf("AllowedEgress = %v, want [broker]", eg)
	}
}
