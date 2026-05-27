package lehelper

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/harness/lite-engine/api"
	lehttp "github.com/harness/lite-engine/cli/client"
	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/app/cloudinit"
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
)

type loggingTransport struct {
	wrapped http.RoundTripper
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {

	resp, err := t.wrapped.RoundTrip(req)
	if err != nil {
		dump, _ := httputil.DumpRequestOut(req, true)
		logrus.WithField("request", string(dump)).Traceln("LE HTTP request")
		logrus.WithError(err).Errorln("LE HTTP request failed")
		return resp, err
	}

	return resp, nil
}

const (
	LiteEnginePort = 9079
)

func GenerateUserdata(userdata string, opts *types.InstanceCreateOpts) (string, error) {
	var params = cloudinit.Params{
		Platform:                     opts.Platform,
		CACert:                       string(opts.CACert),
		TLSCert:                      string(opts.TLSCert),
		TLSKey:                       string(opts.TLSKey),
		LiteEnginePath:               opts.LiteEnginePath,
		LiteEngineLogsPath:           oshelp.GetLiteEngineLogsPath(opts.Platform.OS),
		HarnessTestBinaryURI:         opts.HarnessTestBinaryURI,
		PluginBinaryURI:              opts.PluginBinaryURI,
		Tmate:                        opts.Tmate,
		IsHosted:                     opts.IsHosted,
		AutoInjectionBinaryURI:       opts.AutoInjectionBinaryURI,
		LiteEngineFallbackPath:       opts.LiteEngineFallbackPath,
		PluginBinaryFallbackURI:      opts.PluginBinaryFallbackURI,
		DriverName:                   opts.DriverName,
		EnableC4D:                    opts.EnableC4D,
		AnnotationsBinaryURI:         opts.AnnotationsBinaryURI,
		AnnotationsBinaryFallbackURI: opts.AnnotationsBinaryFallbackURI,
		EnvmanBinaryURI:              opts.EnvmanBinaryURI,
		EnvmanBinaryFallbackURI:      opts.EnvmanBinaryFallbackURI,
		TmateBinaryURI:               opts.TmateBinaryURI,
		TmateBinaryFallbackURI:       opts.TmateBinaryFallbackURI,
	}
	if opts.GitspaceOpts.VMInitScript != "" {
		params.GitspaceAgentConfig = types.GitspaceAgentConfig{
			VMInitScript: opts.GitspaceOpts.VMInitScript,
		}
		params.CertsDirectory = "/harness/certs"
	}

	var err error
	if userdata == "" {
		if opts.Platform.OS == oshelp.OSWindows {
			userdata = cloudinit.Windows(&params)
		} else if opts.Platform.OS == oshelp.OSMac {
			userdata = cloudinit.Mac(&params)
		} else {
			userdata, err = cloudinit.Linux(&params)
		}
	} else {
		userdata, err = cloudinit.Custom(userdata, &params)
	}
	return userdata, err
}

func GetClient(instance *types.Instance, serverName string, liteEnginePort int64, mock bool, mockTimeoutSecs int) (lehttp.Client, error) {
	leURL := fmt.Sprintf("https://%s:%d/", instance.Address, liteEnginePort)
	if mock {
		return lehttp.NewNoopClient(&api.PollStepResponse{}, nil, time.Duration(mockTimeoutSecs)*time.Second, 0, 0), nil
	}
	c, err := lehttp.NewHTTPClient(leURL,
		serverName, string(instance.CACert),
		string(instance.TLSCert), string(instance.TLSKey))
	if err != nil {
		return nil, err
	}
	c.Client.Transport = &loggingTransport{wrapped: c.Client.Transport}
	return c, nil
}
