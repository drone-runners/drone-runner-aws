package lehelper

import (
	"fmt"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/cloudinit"
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/harness/lite-engine/api"
	lehttp "github.com/harness/lite-engine/cli/client"
)

const (
	LiteEnginePort = 9079
)

func GenerateUserdata(userdata string, opts *types.InstanceCreateOpts) (string, error) {
	var params = cloudinit.Params{
		Platform:               opts.Platform,
		CACert:                 string(opts.CACert),
		TLSCert:                string(opts.TLSCert),
		TLSKey:                 string(opts.TLSKey),
		LiteEnginePath:         opts.LiteEnginePath,
		LiteEngineLogsPath:     oshelp.GetLiteEngineLogsPath(opts.Platform.OS),
		HarnessTestBinaryURI:   opts.HarnessTestBinaryURI,
		PluginBinaryURI:        opts.PluginBinaryURI,
		Tmate:                  opts.Tmate,
		IsHosted:               opts.IsHosted,
		AutoInjectionBinaryURI: opts.AutoInjectionBinaryURI,
	}
	if opts.GitspaceOpts.VMInitScript != "" {
		params.GitspaceAgentConfig = types.GitspaceAgentConfig{
			VMInitScript: opts.GitspaceOpts.VMInitScript,
		}
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
	return lehttp.NewHTTPClient(leURL,
		serverName, string(instance.CACert),
		string(instance.TLSCert), string(instance.TLSKey))
}
