package cloudinit_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drone-runners/drone-runner-vm/internal/cloudinit"
	"github.com/drone-runners/drone-runner-vm/types"
)

const (
	liteEnginePath = "/lite/engine/goes/here"
	caCertFile     = "qwerty123"
	certFile       = "abcdef456"
	keyFile        = "xyzuvw789"
)

var (
	platform = types.Platform{
		OS:   "spectrum",
		Arch: "z80",
	}
)

func TestLinux(t *testing.T) {
	params := &cloudinit.Params{
		LiteEnginePath: liteEnginePath,
		CACert:         caCertFile + "\n",
		TLSCert:        certFile + "\n",
		TLSKey:         keyFile + "\n",
		Platform:       platform,
	}

	s := cloudinit.Linux(params)
	lePath := fmt.Sprintf(`"%s/lite-engine-%s-%s"`, params.LiteEnginePath, params.Platform.OS, params.Platform.Arch)
	if !strings.Contains(s, lePath) {
		t.Error("linux init script does not contain LE path")
	}
}

func TestWindows(t *testing.T) {
	params := &cloudinit.Params{
		LiteEnginePath: liteEnginePath,
		CACert:         caCertFile + "\n",
		TLSCert:        certFile + "\n",
		TLSKey:         keyFile + "\n",
	}

	s := cloudinit.Windows(params)
	lePath := fmt.Sprintf(`"%s/lite-engine-%s-%s.exe"`, params.LiteEnginePath, params.Platform.OS, params.Platform.Arch)
	if !strings.Contains(s, lePath) {
		t.Error("windows init script does not contain LE path")
	}
}
