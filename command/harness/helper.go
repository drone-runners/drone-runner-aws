package harness

import (
	"context"
	"hash/fnv"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/harness/lite-engine/engine/spec"

	"github.com/sirupsen/logrus"

	leapi "github.com/harness/lite-engine/api"
	lelivelog "github.com/harness/lite-engine/livelog"
	lestream "github.com/harness/lite-engine/logstream/remote"
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
