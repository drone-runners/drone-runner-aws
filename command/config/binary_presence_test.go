package config

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBinaryPresence(t *testing.T) {
	cfg, err := FromEnviron()
	if err != nil {
		t.Fatalf("FromEnviron failed: %v", err)
	}

	type urlCheck struct {
		label string
		url   string
	}

	join := func(base, filename string) string {
		return strings.TrimRight(base, "/") + "/" + filename
	}

	var checks []urlCheck

	// lite-engine
	for _, filename := range []string{
		"lite-engine-linux-amd64",
		"lite-engine-linux-arm64",
		"lite-engine-darwin-amd64",
		"lite-engine-darwin-arm64",
		"lite-engine-windows-amd64.exe",
	} {
		checks = append(checks,
			urlCheck{"lite-engine primary", join(cfg.LiteEngine.Path, filename)},
			urlCheck{"lite-engine fallback", join(cfg.LiteEngine.FallbackPath, filename)},
		)
	}

	// plugin
	for _, filename := range []string{
		"plugin-linux-amd64",
		"plugin-linux-arm64",
		"plugin-darwin-amd64",
		"plugin-darwin-arm64",
		"plugin-windows-amd64.exe",
	} {
		checks = append(checks,
			urlCheck{"plugin primary", join(cfg.Settings.PluginBinaryURI, filename)},
			urlCheck{"plugin fallback", join(cfg.Settings.PluginBinaryFallbackURI, filename)},
		)
	}

	// auto-injection
	for _, path := range []string{
		"/linux/amd64/auto-injection",
		"/linux/arm64/auto-injection",
		"/darwin/amd64/auto-injection",
		"/darwin/arm64/auto-injection",
		"/windows/amd64/auto-injection",
	} {
		checks = append(checks, urlCheck{"auto-injection", strings.TrimRight(cfg.Settings.AutoInjectionBinaryURI, "/") + path})
	}

	// hcli (annotations)
	for _, filename := range []string{
		"hcli-linux-amd64",
		"hcli-linux-arm64",
		"hcli-darwin-amd64",
		"hcli-darwin-arm64",
		"hcli-windows-amd64.exe",
	} {
		checks = append(checks,
			urlCheck{"hcli primary", join(cfg.Settings.AnnotationsBinaryURI, filename)},
			urlCheck{"hcli fallback", join(cfg.Settings.AnnotationsBinaryFallbackURI, filename)},
		)
	}

	// envman
	for _, filename := range []string{
		"envman-Linux-x86_64",
		"envman-Darwin-arm64",
	} {
		checks = append(checks,
			urlCheck{"envman primary", join(cfg.Settings.EnvmanBinaryURI, filename)},
			urlCheck{"envman fallback", join(cfg.Settings.EnvmanBinaryFallbackURI, filename)},
		)
	}

	// tmate
	for _, filename := range []string{
		"tmate-1.0-static-linux-amd64.tar.xz",
		"tmate-1.0-static-linux-arm64v8.tar.xz",
		"tmate-1.0-static-mac-arm64.tar.xz",
	} {
		checks = append(checks,
			urlCheck{"tmate primary", join(cfg.Settings.TmateBinaryURI, filename)},
			urlCheck{"tmate fallback", join(cfg.Settings.TmateBinaryFallbackURI, filename)},
		)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	failed := false

	for _, c := range checks {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodHead, c.url, http.NoBody)
		if err != nil {
			t.Fatalf("failed to build request for %s: %v", c.url, err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("FAIL [%s] %s: %v", c.label, c.url, err)
			failed = true
			continue
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			t.Logf("FAIL [%s] %s: HTTP %d", c.label, c.url, resp.StatusCode)
			failed = true
		}
	}

	if failed {
		t.Fail()
	}
}
