package lehelper

import (
	"fmt"

	"github.com/drone-runners/drone-runner-vm/internal/cloudinit"
	"github.com/drone-runners/drone-runner-vm/internal/oshelp"
	"github.com/drone-runners/drone-runner-vm/types"
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

func GetClient(instance *types.Instance, runnerName string, liteEnginePort int64) (*lehttp.HTTPClient, error) {
	leURL := fmt.Sprintf("https://%s:%d/", instance.Address, liteEnginePort)
	return lehttp.NewHTTPClient(leURL,
		runnerName, string(instance.CACert),
		string(instance.TLSCert), string(instance.TLSKey))
}
