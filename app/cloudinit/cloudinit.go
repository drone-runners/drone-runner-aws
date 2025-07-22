// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

// Package userdata contains code to generate userdata scripts executed by the cloud-init directive.

package cloudinit

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
)

// Params defines parameters used to create userdata files.
type Params struct {
	LiteEnginePath          string
	LiteEngineLogsPath      string
	CACert                  string
	TLSCert                 string
	TLSKey                  string
	Platform                types.Platform
	HarnessTestBinaryURI    string
	PluginBinaryURI         string
	Tmate                   types.Tmate
	IsHosted                bool
	GitspaceAgentConfig     types.GitspaceAgentConfig
	StorageConfig           types.StorageConfig
	AutoInjectionBinaryURI  string
	LiteEngineFallbackPath  string
	PluginBinaryFallbackURI string
	ShouldUseGoogleDNS      bool
	DriverName              string
	CertsDirectory          string
	IsC4DLSSDEnabled        bool
}

var funcs = map[string]interface{}{
	"base64": func(src string) string {
		return base64.StdEncoding.EncodeToString([]byte(src))
	},
	"trim": strings.TrimSpace,
}

const certsDir = "/tmp/certs/"

// Custom creates a custom userdata file.
func Custom(templateText string, params *Params) (payload string, err error) {
	t, err := template.New("custom-template").Funcs(funcs).Parse(templateText)
	if err != nil {
		err = fmt.Errorf("failed to parse template data: %w", err)
		return "", err
	}

	sb := &strings.Builder{}

	caCertPath := filepath.Join(certsDir, "ca-cert.pem")
	certPath := filepath.Join(certsDir, "server-cert.pem")
	keyPath := filepath.Join(certsDir, "server-key.pem")

	err = t.Execute(sb, struct {
		Params
		CaCertPath string
		CertPath   string
		KeyPath    string
		CertDir    string
	}{
		Params:     *params,
		CaCertPath: caCertPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
		CertDir:    certsDir,
	})
	if err != nil {
		err = fmt.Errorf("failed to execute template to get init script: %w", err)
		return "", err
	}

	payload = sb.String()

	return payload, nil
}

//go:embed user_data/mac
var userDataMac string
var macTemplate = template.Must(template.New("mac").Funcs(funcs).Parse(userDataMac))

//go:embed user_data/mac_arm64
var userDataMacArm64 string
var macArm64Template = template.Must(template.New("mac-arm64").Funcs(funcs).Parse(userDataMacArm64))

//go:embed user_data/nomad_linux
var userDataNomadLinux string
var linuxBashTemplate = template.Must(template.New("linux-bash").Funcs(funcs).Parse(userDataNomadLinux))

//go:embed user_data/gitspaces_linux
var userDataGitspacesLinux string
var gitspacesLinuxTemplate = template.Must(template.New("linux-bash").Funcs(funcs).Parse(userDataGitspacesLinux))

//go:embed user_data/gcp_linux
var userDataGcpLinux string
var ubuntuTemplate = template.Must(template.New(oshelp.OSLinux).Funcs(funcs).Parse(userDataGcpLinux))

//go:embed user_data/gitspaces_ubuntu
var userDataGitspacesUbuntu string
var gitspacesUbuntuTemplate = template.Must(template.New(oshelp.OSLinux).Funcs(funcs).Parse(userDataGitspacesUbuntu))

//go:embed user_data/amazon_gitspaces_ubuntu
var userDataAmazonGitspacesUbuntu string
var gitspacesAWSUbuntuTemplate = template.Must(template.New(oshelp.OSLinux).Funcs(funcs).Parse(userDataAmazonGitspacesUbuntu))

//go:embed user_data/amazon_linux
var userDataAmazonLinux string
var amazonLinuxTemplate = template.Must(template.New(oshelp.OSLinux).Funcs(funcs).Parse(userDataAmazonLinux))

//go:embed user_data/gitspaces_amazon_linux
var userDataGitspacesAmazonLinux string
var gitspacesAmazonLinuxTemplate = template.Must(template.New(oshelp.OSLinux).Funcs(funcs).Parse(userDataGitspacesAmazonLinux))

//go:embed user_data/windows
var userDataWindows string
var windowsTemplate = template.Must(template.New(oshelp.OSWindows).Funcs(funcs).Parse(userDataWindows))

func Mac(params *Params) (payload string) {
	sb := &strings.Builder{}

	caCertPath := filepath.Join(certsDir, "ca-cert.pem")
	certPath := filepath.Join(certsDir, "server-cert.pem")
	keyPath := filepath.Join(certsDir, "server-key.pem")

	var p = struct {
		Params
		CaCertPath string
		CertPath   string
		KeyPath    string
	}{
		Params:     *params,
		CaCertPath: caCertPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
	}

	if params.Platform.Arch == oshelp.ArchARM64 {
		err := macArm64Template.Execute(sb, p)
		if err != nil {
			err = fmt.Errorf("failed to execute mac arm64 template to get init script: %w", err)
			panic(err)
		}
	} else {
		err := macTemplate.Execute(sb, p)
		if err != nil {
			err = fmt.Errorf("failed to execute mac amd64 template to get init script: %w", err)
			panic(err)
		}
	}
	return sb.String()
}

// This generates a bash startup script for linux
func LinuxBash(params *Params) (payload string) {
	sb := &strings.Builder{}

	caCertPath := filepath.Join(certsDir, "ca-cert.pem")
	certPath := filepath.Join(certsDir, "server-cert.pem")
	keyPath := filepath.Join(certsDir, "server-key.pem")

	var p = struct {
		Params
		CaCertPath string
		CertPath   string
		CertDir    string
		KeyPath    string
	}{
		Params:     *params,
		CaCertPath: caCertPath,
		CertDir:    certsDir,
		CertPath:   certPath,
		KeyPath:    keyPath,
	}

	var err error
	if (params.GitspaceAgentConfig.Secret != "" && params.GitspaceAgentConfig.AccessToken != "") ||
		(params.GitspaceAgentConfig.VMInitScript != "") {
		if params.GitspaceAgentConfig.VMInitScript != "" {
			decodedScript, decodeErr := base64.StdEncoding.DecodeString(params.GitspaceAgentConfig.VMInitScript)
			if decodeErr != nil {
				err = fmt.Errorf("failed to decode the gitspaces vm init script: %w", err)
				panic(err)
			}
			p.GitspaceAgentConfig.VMInitScript = string(decodedScript)
		}
		err = gitspacesLinuxTemplate.Execute(sb, p)
	} else {
		err = linuxBashTemplate.Execute(sb, p)
	}
	if err != nil {
		err = fmt.Errorf("failed to execute linux bash template to get init script: %w", err)
		panic(err)
	}
	return sb.String()
}

// Linux creates a userdata file for the Linux operating system.
func Linux(params *Params) (payload string, err error) {

	if params.CertsDirectory == "" {
		params.CertsDirectory = certsDir
	}
	sb := &strings.Builder{}
	caCertPath := filepath.Join(params.CertsDirectory, "ca-cert.pem")
	certPath := filepath.Join(params.CertsDirectory, "server-cert.pem")
	keyPath := filepath.Join(params.CertsDirectory, "server-key.pem")
	templateData := struct {
		Params
		CaCertPath string
		CertPath   string
		KeyPath    string
	}{
		Params:     *params,
		CaCertPath: caCertPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
	}

	// Decode VMInitScript if provided
	if params.GitspaceAgentConfig.VMInitScript != "" {
		decoded, err := base64.StdEncoding.DecodeString(params.GitspaceAgentConfig.VMInitScript)
		if err != nil {
			return "", fmt.Errorf("failed to decode the gitspaces vm init script: %w", err)
		}
		templateData.GitspaceAgentConfig.VMInitScript = string(decoded)
	}

	// Select template
	var tmpl *template.Template
	switch params.Platform.OSName {
	case oshelp.AmazonLinux:
		if params.GitspaceAgentConfig.VMInitScript != "" {
			tmpl = gitspacesAmazonLinuxTemplate
		} else {
			tmpl = amazonLinuxTemplate
		}
	default:
		if params.GitspaceAgentConfig.VMInitScript != "" {
			if params.DriverName == string(types.Amazon) {
				tmpl = gitspacesAWSUbuntuTemplate
			} else {
				tmpl = gitspacesUbuntuTemplate
			}
		} else {
			tmpl = ubuntuTemplate
		}
	}
	// Execute selected template
	if err := tmpl.Execute(sb, templateData); err != nil {
		return "", fmt.Errorf("error while executing template: %w", err)
	}

	return sb.String(), nil
}

// Windows creates a userdata file for the Windows operating system.
func Windows(params *Params) (payload string) {
	sb := &strings.Builder{}

	caCertPath := filepath.Join(certsDir, "ca-cert.pem")
	certPath := filepath.Join(certsDir, "server-cert.pem")
	keyPath := filepath.Join(certsDir, "server-key.pem")

	_ = windowsTemplate.Execute(sb, struct {
		Params
		CertDir    string
		CaCertPath string
		CertPath   string
		KeyPath    string
	}{
		Params:     *params,
		CertDir:    certsDir,
		CaCertPath: caCertPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
	})

	return sb.String()
}
