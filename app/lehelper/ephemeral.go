package lehelper

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/drone-runners/drone-runner-aws/app/cloudinit"
	"github.com/drone-runners/drone-runner-aws/types"
)

const defaultDelegateParallelWorkers = "2"

func applyEphemeralUserdata(params *cloudinit.Params, opts *types.InstanceCreateOpts) error {
	if opts.DelegatePath == "" {
		return fmt.Errorf("delegate download path is required")
	}
	params.DelegatePath = opts.DelegatePath
	params.DelegateFallbackPath = opts.DelegateFallbackPath
	params.EgressControl = opts.EgressControl
	if !opts.EgressControl {
		params.DelegateConfigEnv = renderDelegateConfigEnv(opts, "", "", "")
		return nil
	}
	if opts.EgressProxyURL == "" {
		return fmt.Errorf("egress proxy URL is required")
	}
	if opts.EgressCACert == "" {
		return fmt.Errorf("egress CA certificate is required")
	}
	scheme, host, port, err := splitProxyURL(opts.EgressProxyURL)
	if err != nil {
		return err
	}
	downloadURL, err := credentialedProxyURL(opts.EgressProxyURL, opts.EgressProxyUser, opts.EgressProxyPassword)
	if err != nil {
		return err
	}
	params.DelegateConfigEnv = renderDelegateConfigEnv(opts, scheme, host, port)
	params.DownloadProxyURL = downloadURL
	params.DockerProxyDropIn = dockerProxyDropIn(downloadURL, opts.EgressNoProxy)
	return nil
}

func renderDelegateConfigEnv(opts *types.InstanceCreateOpts, scheme, host, port string) string {
	workers := opts.DelegateParallelWorkers
	if workers == "" {
		workers = defaultDelegateParallelWorkers
	}
	values := map[string]string{
		"HARNESS_DELEGATE_TOKEN":        opts.DelegateToken,
		"HARNESS_ACCOUNT_ID":            opts.AccountID,
		"HARNESS_MANAGER_HOST_AND_PORT": opts.DelegateManagerEndpoint,
		"HARNESS_DELEGATE_NAME":         opts.DelegateName,
		"HARNESS_TRANSACTION_ID":        opts.DelegateTransactionID,
		"HARNESS_PARALLEL_WORKERS":      workers,
		"HARNESS_TAGS":                  opts.DelegateTags,
	}
	if opts.EgressControl {
		values["PROXY_SCHEME"] = scheme
		values["PROXY_HOST"] = host
		values["PROXY_PORT"] = port
		values["PROXY_USER"] = opts.EgressProxyUser
		values["PROXY_PASSWORD"] = opts.EgressProxyPassword
		values["NO_PROXY"] = opts.EgressNoProxy
	}
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if value == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&b, "%s=%s\n", key, values[key])
	}
	return b.String()
}

func dockerProxyDropIn(proxyURL, noProxy string) string {
	return fmt.Sprintf(`[Service]
Environment="HTTP_PROXY=%s"
Environment="HTTPS_PROXY=%s"
Environment="NO_PROXY=%s"
`, proxyURL, proxyURL, noProxy)
}

func splitProxyURL(raw string) (scheme, host, port string, err error) {
	u, err := parseProxyURL(raw)
	if err != nil {
		return "", "", "", err
	}
	if u.Port() == "" {
		return "", "", "", fmt.Errorf("egress proxy URL must include a port")
	}
	return u.Scheme, u.Hostname(), u.Port(), nil
}

func credentialedProxyURL(raw, username, password string) (string, error) {
	u, err := parseProxyURL(raw)
	if err != nil {
		return "", err
	}
	if username != "" && password != "" {
		u.User = url.UserPassword(username, password)
	}
	return u.String(), nil
}

func parseProxyURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, fmt.Errorf("egress proxy URL is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid egress proxy URL %q", raw)
	}
	if u.Scheme == "" {
		u.Scheme = "http"
	}
	return u, nil
}
