package vmfusion

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/drone-runners/drone-runner-vm/internal/drivers"
	"github.com/drone-runners/drone-runner-vm/internal/oshelp"

	"github.com/sirupsen/logrus"
)

func vmrun(args ...string) (string, string, error) { //nolint
	_ = syscall.Umask(022) //nolint
	cmd := exec.Command(vmrunbin, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	logrus.Debugf("executing: %v %v", vmrunbin, strings.Join(args, " "))

	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.Error); ok && ee == exec.ErrNotFound {
			err = ErrVMRUNNotFound
		}
	}

	return stdout.String(), stderr.String(), err
}

func (p *config) GetState() (State, error) {
	vmxp, err := filepath.EvalSymlinks(p.vmxPath())
	if err != nil {
		return Error, err
	}
	if stdout, _, _ := vmrun("list"); strings.Contains(stdout, vmxp) {
		return Running, nil
	}
	return Stopped, nil
}

func (p *config) GetIP() (string, error) {
	s, err := p.GetState()
	if err != nil {
		return "", err
	}
	if s != Running {
		return "", drivers.ErrHostIsNotRunning
	}

	// determine MAC address for VM
	macaddr, err := p.getMacAddressFromVmx()
	if err != nil {
		return "", err
	}

	// attempt to find the address in the vmnet configuration
	var ip string
	if ip, err = p.getIPfromVmnetConfiguration(macaddr); err == nil {
		return ip, nil
	}

	// address not found in vmnet so look for a DHCP lease
	ip, err = p.getIPFromDHCPLease(macaddr)
	if err != nil {
		return "", err
	}

	return ip, nil
}

func (p *config) getMacAddressFromVmx() (string, error) {
	var vmxfh *os.File
	var vmxcontent []byte
	var err error

	if vmxfh, err = os.Open(p.vmxPath()); err != nil {
		return "", err
	}
	defer vmxfh.Close()

	if vmxcontent, err = io.ReadAll(vmxfh); err != nil {
		return "", err
	}

	// Look for generatedAddress as we're passing a VMX with addressType = "generated".
	var macaddr string
	vmxparse := regexp.MustCompile(`^ethernet0.generatedAddress\s*=\s*"(.*?)"\s*$`)
	for _, line := range strings.Split(string(vmxcontent), "\n") {
		if matches := vmxparse.FindStringSubmatch(line); matches == nil {
			continue
		} else {
			macaddr = strings.ToLower(matches[1])
		}
	}

	if macaddr == "" {
		return "", fmt.Errorf("couldn't find MAC address in VMX file %s", p.vmxPath())
	}

	logrus.Debugf("MAC address in VMX: %s", macaddr)

	return macaddr, nil
}

func (p *config) getIPfromVmnetConfiguration(macaddr string) (string, error) {
	// DHCP lease table for NAT vmnet interface
	confFiles, _ := filepath.Glob("/Library/Preferences/VMware Fusion/vmnet*/dhcpd.conf")
	for _, conffile := range confFiles {
		logrus.Debugf("Trying to find IP address in configuration file: %s", conffile)
		if ipaddr, err := p.getIPfromVmnetConfigurationFile(conffile, macaddr); err == nil {
			return ipaddr, nil
		}
	}

	return "", fmt.Errorf("IP not found for MAC %s in vmnet configuration files", macaddr)
}

func (p *config) getIPfromVmnetConfigurationFile(conffile, macaddr string) (string, error) {
	var conffh *os.File
	var confcontent []byte

	var currentip string
	var lastipmatch string
	var lastmacmatch string

	var err error

	if conffh, err = os.Open(conffile); err != nil {
		return "", err
	}
	defer conffh.Close()

	if confcontent, err = io.ReadAll(conffh); err != nil {
		return "", err
	}

	// find all occurrences of 'host .* { .. }' and extract
	// out of the inner block the MAC and IP addresses

	// key = MAC, value = IP
	m := make(map[string]string)

	// Begin of a host block, that contains the IP, MAC
	hostbegin := regexp.MustCompile(`^host (.+?) {`)
	// End of a host block
	hostend := regexp.MustCompile(`^}`)

	// Get the IP address.
	ip := regexp.MustCompile(`^\s*fixed-address (.+?);$`)
	// Get the MAC address associated.
	mac := regexp.MustCompile(`^\s*hardware ethernet (.+?);$`)

	// we use a block depth so that just in case inner blocks exists
	// we are not being fooled by them
	blockdepth := 0
	for _, line := range strings.Split(string(confcontent), "\n") {
		if matches := hostbegin.FindStringSubmatch(line); matches != nil {
			blockdepth++
			continue
		}

		// we are only in interested in endings if we in a block. Otherwise we will count
		// ending of non host blocks as well
		if matches := hostend.FindStringSubmatch(line); blockdepth > 0 && matches != nil {
			blockdepth--

			if blockdepth == 0 {
				// add data
				m[lastmacmatch] = lastipmatch

				// reset all temp var holders
				lastipmatch = ""
				lastmacmatch = ""
			}

			continue
		}

		// only if we are within the first level of a block
		// we are looking for addresses to extract
		if blockdepth == 1 {
			if matches := ip.FindStringSubmatch(line); matches != nil {
				lastipmatch = matches[1]
				continue
			}

			if matches := mac.FindStringSubmatch(line); matches != nil {
				lastmacmatch = strings.ToLower(matches[1])
				continue
			}
		}
	}

	logrus.Debugf("Following IPs found %s", m)

	// map is filled to now lets check if we have a MAC associated to an IP
	currentip, ok := m[strings.ToLower(macaddr)]

	if !ok {
		return "", fmt.Errorf("IP not found for MAC %s in vmnet configuration", macaddr)
	}

	logrus.Debugf("IP found in vmnet configuration file: %s", currentip)

	return currentip, nil
}

func (p *config) getIPFromDHCPLease(macaddr string) (string, error) {
	// DHCP lease table for NAT vmnet interface
	leasesFiles, _ := filepath.Glob("/var/db/vmware/*.leases")
	for _, dhcpfile := range leasesFiles {
		logrus.Debugf("Trying to find IP address in leases file: %s", dhcpfile)
		if ipaddr, err := p.getIPfromDHCPLeaseFile(dhcpfile, macaddr); err == nil {
			return ipaddr, nil
		}
	}

	return "", fmt.Errorf("IP not found for MAC %s in DHCP leases", macaddr)
}

func (p *config) getIPfromDHCPLeaseFile(dhcpfile, macaddr string) (string, error) {
	var dhcpfh *os.File
	var dhcpcontent []byte
	var lastipmatch string
	var currentip string
	var lastleaseendtime time.Time
	var currentleadeendtime time.Time
	var err error

	if dhcpfh, err = os.Open(dhcpfile); err != nil {
		return "", err
	}
	defer dhcpfh.Close()

	if dhcpcontent, err = io.ReadAll(dhcpfh); err != nil {
		return "", err
	}

	// Get the IP from the lease table.
	leaseip := regexp.MustCompile(`^lease (.+?) {$`)
	// Get the lease end date time.
	leaseend := regexp.MustCompile(`^\s*ends \d (.+?);$`)
	// Get the MAC address associated.
	leasemac := regexp.MustCompile(`^\s*hardware ethernet (.+?);$`)

	for _, line := range strings.Split(string(dhcpcontent), "\n") {
		if matches := leaseip.FindStringSubmatch(line); matches != nil {
			lastipmatch = matches[1]
			continue
		}

		if matches := leaseend.FindStringSubmatch(line); matches != nil {
			lastleaseendtime, _ = time.Parse("2006/01/02 15:04:05", matches[1])
			continue
		}

		if matches := leasemac.FindStringSubmatch(line); matches != nil && matches[1] == macaddr && currentleadeendtime.Before(lastleaseendtime) { //nolint
			currentip = lastipmatch
			currentleadeendtime = lastleaseendtime
		}
	}

	if currentip == "" {
		return "", fmt.Errorf("IP not found for MAC %s in DHCP leases", macaddr)
	}

	logrus.Debugf("IP found in DHCP lease table: %s", currentip)

	return currentip, nil
}

func setVmwareCmd(cmd string) string {
	if path, err := exec.LookPath(cmd); err == nil {
		return path
	}
	path := []string{"/", "Applications", "VMware Fusion.app", "Contents", "Library", cmd}
	return filepath.Join(path...)
}

func (p *config) vmxPath() string {
	return p.ResolveStorePath(fmt.Sprintf("%s.vmx", p.MachineName))
}

func (p *config) ResolveStorePath(file string) string {
	return filepath.Join(p.StorePath, fmt.Sprintf("%s.vmwarevm", p.MachineName), file)
}

// helper function returns the base temporary directory based on the target platform.
func tempdir(inputOS string) string {
	const dir = "fusion"

	switch inputOS {
	case oshelp.OSWindows:
		return oshelp.JoinPaths(inputOS, "C:\\Windows\\Temp", dir)
	case oshelp.OSMac:
		return oshelp.JoinPaths(inputOS, "/tmp", dir)
	default:
		return oshelp.JoinPaths(inputOS, "/tmp", dir)
	}
}
