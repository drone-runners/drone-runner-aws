package nomad

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/cloudinit"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	cf "github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/hashicorp/nomad/api"
)

//go:embed gitspace/scripts/provision_ceph_storage.sh
var provisionCephStorageScript string

type LinuxVirtualizer struct {
	nomadConfig *types.NomadConfig
}

func NewLinuxVirtualizer(nomadConfig *types.NomadConfig) *LinuxVirtualizer {
	return &LinuxVirtualizer{
		nomadConfig: nomadConfig,
	}
}

var funcs = map[string]interface{}{
	"base64": func(src string) string {
		return base64.StdEncoding.EncodeToString([]byte(src))
	},
	"trim": strings.TrimSpace,
}

func (lv *LinuxVirtualizer) GetInitJob(vm, nodeID, userData, machinePassword, defaultVMImage string, vmImageConfig types.VMImageConfig, port int, resource cf.NomadResource, opts *types.InstanceCreateOpts, gitspacesPortMappings map[int]int, timeout int64) (job *api.Job, id, group string, err error) { //nolint
	id = initJobID(vm)
	group = fmt.Sprintf("init_task_group_%s", vm)
	uData := lv.generateUserData(opts)
	encodedUserData := base64.StdEncoding.EncodeToString([]byte(uData))

	hostPath := fmt.Sprintf("/usr/local/bin/%s.sh", vm)
	vmPath := fmt.Sprintf("/usr/bin/%s.sh", vm)
	var runCmdFormat string
	runCmdFormat = "%s run %s --name %s --cpus %s --memory %sGB --size %s --ssh --runtime=docker --ports %d:%s --copy-files %s:%s"
	args := []interface{}{ignitePath, vmImageConfig.ImageName, vm, resource.Cpus, resource.MemoryGB, resource.DiskSize, port, strconv.Itoa(lehelper.LiteEnginePort), hostPath, vmPath}

	// add labels
	if len(opts.Labels) > 0 {
		runCmdFormat += " --label"
		for key, value := range opts.Labels {
			runCmdFormat += fmt.Sprintf(" %s=%s", key, value)
		}
	}

	if timeout > 0 {
		runCmdFormat += fmt.Sprintf(" --label ttl=%d", timeout)
	}

	// gitspace args
	for vmPort, hostPort := range gitspacesPortMappings {
		runCmdFormat += " --ports %d:%d"
		args = append(args, hostPort, vmPort)
	}

	// gitspace storage args
	var provisionCephStorageScriptPath string
	var storageTask *api.Task
	if opts.StorageOpts.Identifier != "" {
		runCmdFormat = "cat %s | base64 --decode | bash; " + runCmdFormat
		runCmdFormat += " --volumes $(findmnt -no SOURCE /%s):/mnt/disks/mountdevcontainer"
		args = append(args, opts.StorageOpts.Identifier)
		provisionCephStorageScriptPath = fmt.Sprintf("/usr/local/bin/%s_provision_ceph_storage.sh", vm)
		args = append([]interface{}{provisionCephStorageScriptPath}, args...)
		storageTask = lv.getCephStorageTask(opts.StorageOpts.CephPoolIdentifier, opts.StorageOpts.Identifier, provisionCephStorageScriptPath, opts.StorageOpts.Size)
	}

	runCmd := fmt.Sprintf(runCmdFormat, args...)
	cleanUpCmd := lv.getScriptCleanupCmd(opts, hostPath, provisionCephStorageScriptPath)
	entrypoint := lv.GetEntryPoint()

	job = &api.Job{
		ID:          &id,
		Name:        stringToPtr(vm),
		Type:        stringToPtr("batch"),
		Datacenters: []string{"dc1"},
		Constraints: []*api.Constraint{
			{
				LTarget: "${node.unique.id}",
				RTarget: nodeID,
				Operand: "=",
			},
		},
		Reschedule: &api.ReschedulePolicy{
			Attempts:  intToPtr(0),
			Unlimited: boolToPtr(false),
		},
		TaskGroups: []*api.TaskGroup{
			{
				StopAfterClientDisconnect: &lv.nomadConfig.ClientDisconnectTimeout,
				RestartPolicy: &api.RestartPolicy{
					Attempts: intToPtr(0),
				},
				Name:  stringToPtr(group),
				Count: intToPtr(1),
				Tasks: []*api.Task{
					{
						Name:      "create_startup_script_on_host",
						Driver:    "raw_exec",
						Resources: minNomadResources(lv.nomadConfig.MinNomadCPUMhz, lv.nomadConfig.MinNomadMemoryMb),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", fmt.Sprintf("echo %s >> %s", encodedUserData, hostPath)},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "prestart",
						},
					},

					{
						Name:      "enable_port_forwarding",
						Driver:    "raw_exec",
						Resources: minNomadResources(lv.nomadConfig.MinNomadCPUMhz, lv.nomadConfig.MinNomadMemoryMb),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", "iptables -P FORWARD ACCEPT"},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "prestart",
						},
					},
					{
						Name:      "ignite_run",
						Driver:    "raw_exec",
						Resources: minNomadResources(lv.nomadConfig.MinNomadCPUMhz, lv.nomadConfig.MinNomadMemoryMb),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", runCmd},
						},
					},
					{
						Name:      "wait_for_ignite_ready",
						Driver:    "raw_exec",
						Resources: minNomadResources(lv.nomadConfig.MinNomadCPUMhz, lv.nomadConfig.MinNomadMemoryMb),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args": []string{"-c", fmt.Sprintf(`
								for i in {1..10}; do
									if %s exec %s echo "hello" >/dev/null 2>&1; then
										echo "ignite exec success for VM %s"
										exit 0
									fi
									sleep 1
								done
								echo "ignite exec failed for VM %s" >&2
								exit 1
							`, ignitePath, vm, vm, vm)},
						},
					},
					{
						Name:      "ignite_exec",
						Driver:    "raw_exec",
						Resources: minNomadResources(lv.nomadConfig.MinNomadCPUMhz, lv.nomadConfig.MinNomadMemoryMb),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", fmt.Sprintf("%s exec %s 'cat %s | base64 --decode | bash'", ignitePath, vm, vmPath)},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "poststop",
						},
					},
					{
						Name:      "cleanup_startup_script_from_host",
						Driver:    "raw_exec",
						Resources: minNomadResources(lv.nomadConfig.MinNomadCPUMhz, lv.nomadConfig.MinNomadMemoryMb),
						Config: map[string]interface{}{
							"command": entrypoint,
							"args":    []string{"-c", cleanUpCmd},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "poststop",
						},
					},
				},
			},
		},
	}
	if opts.StorageOpts.Identifier != "" && storageTask != nil {
		job.TaskGroups[0].Tasks = append([]*api.Task{storageTask}, job.TaskGroups[0].Tasks...)
	}
	return job, id, group, err
}

func (lv *LinuxVirtualizer) getCephStorageTask(
	cephPoolIdentifier string,
	rbdIdentifier string,
	provisionCephStorageScriptPath string,
	diskSize string,
) *api.Task {
	provisionCephStorageTemplate := template.Must(template.New("provision-ceph-storage").Funcs(funcs).Parse(provisionCephStorageScript))
	sb := &strings.Builder{}
	params := struct {
		CephPoolIdentifier string
		RBDIdentifier      string
		Size               string
	}{
		CephPoolIdentifier: cephPoolIdentifier,
		RBDIdentifier:      rbdIdentifier,
		Size:               diskSize,
	}
	err := provisionCephStorageTemplate.Execute(sb, params)
	if err != nil {
		err = fmt.Errorf("failed to execute provision-ceph-storage template to get the script: %w", err)
		panic(err)
	}
	provisionCephStorageScriptEncoded := base64.StdEncoding.EncodeToString([]byte(sb.String()))
	return &api.Task{
		Name:      "create_ceph_storage_script_on_host",
		Driver:    "raw_exec",
		Resources: minNomadResources(lv.nomadConfig.MinNomadCPUMhz, lv.nomadConfig.MinNomadMemoryMb),
		Config: map[string]interface{}{
			"command": lv.GetEntryPoint(),
			"args": []string{
				"-c",
				fmt.Sprintf(`echo %s >> %s`, provisionCephStorageScriptEncoded, provisionCephStorageScriptPath),
			},
		},
		Lifecycle: &api.TaskLifecycle{
			Sidecar: false,
			Hook:    "prestart",
		},
	}
}

func (lv *LinuxVirtualizer) generateUserData(opts *types.InstanceCreateOpts) string {
	params := &cloudinit.Params{
		Platform:                opts.Platform,
		CACert:                  string(opts.CACert),
		LiteEngineLogsPath:      oshelp.GetLiteEngineLogsPath(opts.OS),
		TLSCert:                 string(opts.TLSCert),
		TLSKey:                  string(opts.TLSKey),
		LiteEnginePath:          opts.LiteEnginePath,
		HarnessTestBinaryURI:    opts.HarnessTestBinaryURI,
		PluginBinaryURI:         opts.PluginBinaryURI,
		Tmate:                   opts.Tmate,
		AutoInjectionBinaryURI:  opts.AutoInjectionBinaryURI,
		LiteEngineFallbackPath:  opts.LiteEngineFallbackPath,
		PluginBinaryFallbackURI: opts.PluginBinaryFallbackURI,
	}
	if (opts.GitspaceOpts.Secret != "" && opts.GitspaceOpts.AccessToken != "") ||
		(opts.GitspaceOpts.VMInitScript != "") {
		params.GitspaceAgentConfig = types.GitspaceAgentConfig{
			Secret:       opts.GitspaceOpts.Secret,
			AccessToken:  opts.GitspaceOpts.AccessToken,
			VMInitScript: opts.GitspaceOpts.VMInitScript,
		}
	}
	return cloudinit.LinuxBash(params)
}

func (lv *LinuxVirtualizer) GetMachineFrequency() int {
	return lv.nomadConfig.MachineFrequencyMhz
}

func (lv *LinuxVirtualizer) GetGlobalAccountID() string {
	return lv.nomadConfig.GlobalAccount
}

func (lv *LinuxVirtualizer) GetEntryPoint() string {
	return "/usr/bin/su"
}

func (lv *LinuxVirtualizer) GetHealthCheckupGenerator() func(time.Duration, string, string) string {
	return func(sleep time.Duration, vm, port string) string {
		sleepSecs := sleep.Seconds()
		return fmt.Sprintf(`
#!/usr/bin/bash
sleep %f
echo "done sleeping, port is: %s"
cntr=0
while true
	do
		nc -vz localhost %s
		if [ $? -eq 1 ]; then
		    echo "port check failed, incrementing counter:"
			echo "cntr: "$cntr
			((cntr++))
			if [ $cntr -eq 3 ]; then
				echo "port check failed three times. output of ignite command:"
				ignite ps
				echo "output of iptables:"
				iptables -L -v -n
				exit 1
			fi
		else
			cntr=0
			echo "Port check passed..."
		fi
		sleep 20
	done`, sleepSecs, port, port)
	}
}

func (lv *LinuxVirtualizer) GetDestroyScriptGenerator() func(string, string) string {
	return func(vm, machinePassword string) string {
		return fmt.Sprintf(`
	    %s stop %s; %s rm %s
		if [ $? -ne 0 ]; then
		  %s stop -f %s; %s rm -f %s
		fi
	`, ignitePath, vm, ignitePath, vm, ignitePath, vm, ignitePath, vm)
	}
}

func (lv *LinuxVirtualizer) getScriptCleanupCmd(opts *types.InstanceCreateOpts, hostPath, provisionCephStorageScriptPath string) string {
	cleanUpCmdFormat := "rm %s"
	cleanUpCmdArgs := []interface{}{hostPath}
	if opts.StorageOpts.Identifier != "" && provisionCephStorageScriptPath != "" {
		cleanUpCmdFormat += " %s"
		cleanUpCmdArgs = append(cleanUpCmdArgs, provisionCephStorageScriptPath)
	}
	return fmt.Sprintf(cleanUpCmdFormat, cleanUpCmdArgs...)
}

func (lv *LinuxVirtualizer) GetHealthCheckPort(portLabel string) string {
	return fmt.Sprintf("$NOMAD_PORT_%s", portLabel)
}

func (lv *LinuxVirtualizer) GetInitJobTimeout(vmImageConfig types.VMImageConfig) time.Duration { //nolint
	return lv.nomadConfig.InitTimeout
}
