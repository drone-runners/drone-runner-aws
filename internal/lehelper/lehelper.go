package lehelper

import (
	"fmt"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/harness/lite-engine/api"
	lehttp "github.com/harness/lite-engine/cli/client"
)

const (
	LiteEnginePort = 9079
)

func GenerateUserdata(userdata string, opts *types.InstanceCreateOpts) string {
	var params = cloudinit.Params{
		Platform:             opts.Platform,
		CACert:               string(opts.CACert),
		TLSCert:              string(opts.TLSCert),
		TLSKey:               string(opts.TLSKey),
		LiteEnginePath:       opts.LiteEnginePath,
		HarnessTestBinaryURI: opts.HarnessTestBinaryURI,
		PluginBinaryURI:      opts.PluginBinaryURI,
		Tmate:                opts.Tmate,
	}

	if userdata == "" {
		if opts.OS == oshelp.OSWindows {
			userdata = cloudinit.Windows(&params)
		} else if opts.OS == oshelp.OSMac {
			userdata = cloudinit.Mac(&params)
		} else {
			userdata = cloudinit.Linux(&params)
		}
	} else {
		userdata, _ = cloudinit.Custom(userdata, &params)
	}
	return userdata
}

func GetClient(instance *types.Instance, runnerName string, liteEnginePort int64, mock bool, mockTimeoutSecs int) (lehttp.Client, error) {
	leURL := fmt.Sprintf("https://%s:%d/", instance.Address, liteEnginePort)
	if mock {
		return lehttp.NewNoopClient(&api.PollStepResponse{}, nil, time.Duration(mockTimeoutSecs)*time.Second, 0, 0), nil
	}
	return lehttp.NewHTTPClient(leURL,
		runnerName, string(instance.CACert),
		string(instance.TLSCert), string(instance.TLSKey))
}
