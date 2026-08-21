package harness

import (
	"context"
	"hash/fnv"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/harness/lite-engine/engine/spec"

	"github.com/sirupsen/logrus"

	leapi "github.com/harness/lite-engine/api"
	lelivelog "github.com/harness/lite-engine/livelog"
	lestream "github.com/harness/lite-engine/logstream/remote"
)

// egressCAHostPath is where cloud-init writes the mitm CA on egress-control VMs.
// egressCABundleHostPath is the merged trust bundle (system roots + mitm CA)
// for tools whose CA env vars replace the default trust store.
const (
	egressCAHostPath              = "/etc/harness-certs/ca.crt"
	egressCABundleHostPath        = "/etc/harness-certs/ca-bundle.crt"
	egressCAWindowsHostPath       = "C:\\harness-certs\\ca.crt"
	egressCABundleWindowsHostPath = "C:\\harness-certs\\ca-bundle.crt"
)

func getStreamLogger(cfg *leapi.LogConfig, mtlsConfig spec.MtlsConfig, logKey, correlationID string) *lelivelog.Writer {
	client := lestream.NewHTTPClient(cfg.URL, cfg.AccountID,
		cfg.Token, cfg.IndirectUpload, false, mtlsConfig.ClientCert, mtlsConfig.ClientCertKey)
	wc := lelivelog.New(context.Background(), client, logKey, correlationID, nil, true, cfg.TrimNewLineSuffix, cfg.SkipOpeningStream, cfg.SkipClosingStream)
	go func() {
		if err := wc.Open(); err != nil {
			logrus.WithError(err).Debugln("failed to open log stream")
		}
	}()
	return wc
}

// generate a id from the filename
// /path/to/a.txt and /other/path/to/a.txt should generate different hashes
// eg - a-txt10098 and a-txt-270089
func fileID(filename string) string {
	h := fnv.New32a()
	h.Write([]byte(filename))
	return strings.Replace(filepath.Base(filename), ".", "-", -1) + strconv.Itoa(int(h.Sum32()))
}

// mergeEgressPolicy combines ci-manager-provided credentials with the runner-known proxy URL
// and noProxy list. Fields are passed through raw — credential embedding into the URL happens
// at the consumer (lite-engine for docker setup, configureEgressStep for step env vars).
func mergeEgressPolicy(existing *leapi.EgressPolicy, proxyURL, noProxy string) *leapi.EgressPolicy {
	if proxyURL == "" {
		return existing
	}
	if existing == nil {
		return &leapi.EgressPolicy{ProxyURL: proxyURL, NoProxy: noProxy}
	}
	return &leapi.EgressPolicy{
		Username: existing.Username,
		Password: existing.Password,
		ProxyURL: proxyURL,
		NoProxy:  noProxy,
	}
}

// buildProxyURL embeds credentials into the proxy URL using net/url so that
// special characters (@ : % $ etc.) in credentials are percent-encoded correctly.
// Bare URLs without a scheme are treated as http://.
func buildProxyURL(username, password, proxyURL string) string {
	if username == "" || password == "" {
		return proxyURL
	}
	raw := proxyURL
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return proxyURL
	}
	u.User = url.UserPassword(username, password)
	return u.String()
}
