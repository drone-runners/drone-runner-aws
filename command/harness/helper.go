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
const (
	egressCAHostPath        = "/etc/harness-certs/ca.crt"
	egressCAWindowsHostPath = "C:\\harness-certs\\ca.crt"
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
