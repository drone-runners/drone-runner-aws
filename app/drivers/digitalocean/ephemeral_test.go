package digitalocean

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/digitalocean/godo"
)

func testGodo(t *testing.T, h http.Handler) *godo.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	client := godo.NewClient(srv.Client())
	base, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	client.BaseURL = base
	return client
}

func TestGetEphemeralFirewallIDUsesConfiguredFirewall(t *testing.T) {
	p := &config{configuredFirewallID: "fw-pool", FirewallID: "fw-lite-engine"}
	client := testGodo(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	id, err := p.getEphemeralFirewallID(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if id != "fw-pool" || p.FirewallID != "fw-lite-engine" {
		t.Fatalf("id=%q firewall cache=%q", id, p.FirewallID)
	}
}

func TestGetEphemeralFirewallID(t *testing.T) {
	tests := []struct {
		name     string
		listed   string
		sshKeys  []string
		wantID   string
		wantPort string
	}{
		{
			name:   "reuses the ephemeral firewall and ignores the lite-engine one",
			listed: `{"firewalls":[{"id":"fw-le","name":"harness-runner"},{"id":"fw-eph","name":"harness-runner-ephemeral"}]}`,
			wantID: "fw-eph",
		},
		{
			name:     "creates a firewall with no inbound rules",
			listed:   `{"firewalls":[{"id":"fw-le","name":"harness-runner"}]}`,
			wantID:   "fw-new",
			wantPort: "",
		},
		{
			name:     "creates a firewall with ssh only when keys are set",
			listed:   `{"firewalls":[]}`,
			sshKeys:  []string{"aa:bb"},
			wantID:   "fw-new",
			wantPort: "22",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			client := testGodo(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v2/firewalls":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.listed))
				case r.Method == http.MethodPost && r.URL.Path == "/v2/firewalls":
					var req godo.FirewallRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Errorf("decode create: %v", err)
					}
					if req.Name != ephemeralFirewallName {
						t.Errorf("name = %q", req.Name)
					}
					switch tc.wantPort {
					case "":
						if len(req.InboundRules) != 0 {
							t.Errorf("inbound = %+v, want none", req.InboundRules)
						}
					default:
						if len(req.InboundRules) != 1 || req.InboundRules[0].PortRange != tc.wantPort {
							t.Errorf("inbound = %+v, want port %s", req.InboundRules, tc.wantPort)
						}
					}
					for _, rule := range req.InboundRules {
						if rule.PortRange == "9079" {
							t.Error("ephemeral firewall must not open 9079")
						}
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"firewall":{"id":"fw-new","name":"harness-runner-ephemeral"}}`))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
					http.NotFound(w, r)
				}
			}))
			p := &config{SSHKeys: tc.sshKeys, FirewallID: "fw-cached-lite-engine"}
			id, err := p.getEphemeralFirewallID(context.Background(), client)
			if err != nil {
				t.Fatal(err)
			}
			if id != tc.wantID {
				t.Fatalf("id = %q, want %q", id, tc.wantID)
			}
			if p.FirewallID != "fw-cached-lite-engine" {
				t.Fatalf("lite-engine firewall cache changed to %q", p.FirewallID)
			}
			again, err := p.getEphemeralFirewallID(context.Background(), client)
			if err != nil || again != id {
				t.Fatalf("cached id = %q, %v", again, err)
			}
			if strings.Contains(tc.name, "creates") && calls != 2 {
				t.Fatalf("api calls = %d, want 2 (list + create); cache must skip the second lookup", calls)
			}
			if strings.Contains(tc.name, "reuses") && calls != 1 {
				t.Fatalf("api calls = %d, want 1 list", calls)
			}
		})
	}
}
