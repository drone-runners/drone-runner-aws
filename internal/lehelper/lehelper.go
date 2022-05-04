package lehelper

import (
	"fmt"

	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
	lehttp "github.com/harness/lite-engine/cli/client"
)

var (
	LiteEnginePort = int64(9079) //nolint:gomnd
)

func GenerateUserdata(userdata string, opts *types.InstanceCreateOpts) string {
	var params = cloudinit.Params{
		Architecture:   opts.Arch,
		Platform:       opts.OS,
		CACert:         string(opts.CACert),
		TLSCert:        string(opts.TLSCert),
		TLSKey:         string(opts.TLSKey),
		LiteEnginePath: opts.LiteEnginePath,
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

func GetClient(instance *types.Instance, runnerName string) (*lehttp.HTTPClient, error) {
	leURL := fmt.Sprintf("https://%s:%d/", instance.Address, LiteEnginePort)

	return lehttp.NewHTTPClient(leURL,
		runnerName, string(instance.CACert),
		string(instance.TLSCert), string(instance.TLSKey))
}
