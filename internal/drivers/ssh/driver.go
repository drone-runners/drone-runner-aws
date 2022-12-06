package ssh

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/dchest/uniuri"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/phayes/freeport"
	"golang.org/x/crypto/ssh"
)

type config struct {
	hostname string
	username string
	password string
	sshkey   string
}

func SetPlatformDefaults(platform *types.Platform) (*types.Platform, error) {
	if platform.Arch == "" {
		platform.Arch = oshelp.ArchAMD64
	}
	if platform.Arch != oshelp.ArchAMD64 && platform.Arch != oshelp.ArchARM64 {
		return platform, fmt.Errorf("invalid arch %s, has to be '%s/%s'", platform.Arch, oshelp.ArchAMD64, oshelp.ArchARM64)
	}
	// verify that we are using sane values for OS
	if platform.OS == "" {
		platform.OS = oshelp.OSLinux
	}
	if platform.OS != oshelp.OSLinux && platform.OS != oshelp.OSWindows {
		return platform, fmt.Errorf("invalid OS %s, has to be either'%s/%s'", platform.OS, oshelp.OSLinux, oshelp.OSWindows)
	}

	return platform, nil
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

func (p *config) DriverName() string {
	return ""
}

func (p *config) RootDir() string {
	return ""
}

func (p *config) CanHibernate() bool {
	return false
}

// Ping checks that we can ping the machine
func (p *config) Ping(ctx context.Context) error {
	client, err := dial(p.hostname, p.username, p.password, p.sshkey)
	if err != nil {
		return err
	}
	defer client.Close()
	return nil
}

// Create a VM in the bare metal machine
func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	c, err := dial(p.hostname, p.username, p.password, p.sshkey)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	session, err := c.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	name := opts.RunnerName + "-" + uniuri.NewLen(13) //nolint
	name = strings.ToLower(name)

	var b bytes.Buffer
	session.Stdout = &b
	session.Stderr = &b
	fmt.Println("trying to create a VM ... ")
	port, err := freeport.GetFreePort()
	if err != nil {
		return nil, err
	}
	cmd := fmt.Sprintf("sudo ignite run vistaarjuneja/demo:lite-engine-metal1 --name %s --cpus 2 --memory 6GB --size 6GB --ssh --ports %d:9079", name, port)
	fmt.Printf("cmd is: %s\n", cmd)
	if err := session.Run(cmd); err != nil {
		fmt.Println("stdout/stderr logs: ", b.String())
		return nil, err
	}
	session.Close()

	fmt.Println("successfully created VM, now setting up lite engine server on the VM ... ")

	session, err = c.NewSession()
	if err != nil {
		return nil, err
	}
	session.Stdout = &b
	session.Stderr = &b
	defer session.Close()
	cmd = fmt.Sprintf("sudo ignite exec %s '/usr/bin/lite-engine-linux-amd64 server --env-file=/usr/bin/.env > /dev/null 2>&1 &'", name)
	fmt.Printf("cmd is: %s", cmd)
	if err = session.Run(cmd); err != nil {
		fmt.Println("stdout/stderr logs: ", b.String())
		return nil, err
	}
	fmt.Println("stdout/stderr logs: ", b.String())
	fmt.Println("successfully started up lite engine server on VM ... ")

	return &types.Instance{
		ID:       name,
		Name:     name,
		Platform: opts.Platform,
		State:    types.StateCreated,
		Address:  fmt.Sprintf("http://localhost:%d", port),
		Provider: types.SSH,
		Pool:     opts.PoolName,
	}, nil

	// storage := cache.NewCache(
	// 	storage.NewGenericStorage(
	// 		storage.NewGenericRawStorage("/tmp"), scheme.Serializer))

	// client := client.NewClient(storage)
	// providers.Client = client
	// l, e := client.VMs().List()
	// if e != nil {
	// 	return nil, e
	// }
	// fmt.Printf("list of vms: %+v\n", l)
	// for _, k := range l {
	// 	fmt.Printf("vm: %+v\n", k)
	// }

	// fmt.Println("did something.... ")

	// rf := &run.RunFlags{
	// 	CreateFlags: &run.CreateFlags{},
	// 	StartFlags:  &run.StartFlags{},
	// }

	// args := []string{"vistaarjuneja/demo:lite-engine-metal1"}
	// fs := flag.NewFlagSet("fs", flag.ContinueOnError)
	// fs.Set("name", name)
	// fs.Set("cpus", "2")
	// fs.Set("memory", "2GB")
	// fs.Set("size", "6GB")
	// fs.Set("ssh", "true")
	// rfo, err := rf.NewRunOptions(args, fs)
	// if err != nil {
	// 	return nil, err
	// }

	// err = run.Run(rfo, fs)
	// fmt.Printf("rfo is: %+v\n", rfo)

	// if err != nil {
	// 	return nil, err
	// }
}

// Destroy destroys the VM in the bare metal machine
func (p *config) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	c, err := dial(p.hostname, p.username, p.password, p.sshkey)
	if err != nil {
		return err
	}
	defer c.Close()
	for _, i := range instanceIDs {
		fmt.Printf("trying to delete VM: %s\n", i)
		session, err := c.NewSession()
		if err != nil {
			return err
		}
		var b bytes.Buffer
		session.Stdout = &b
		session.Stderr = &b
		cmd := fmt.Sprintf("sudo ignite kill %s", i)
		if err = session.Run(cmd); err != nil {
			fmt.Println("stdout/stderr logs: ", b.String())
			return err
		}
		fmt.Println("stdout/stderr logs: ", b.String())
		session.Close()
	}
	return nil
}

func (p *config) Logs(ctx context.Context, instanceID string) (string, error) {
	return "", nil
}

func (p *config) SetTags(ctx context.Context, instance *types.Instance,
	tags map[string]string) error {
	return nil
}

func (p *config) Hibernate(ctx context.Context, instanceID, poolName string) error {
	return nil
}

func (p *config) Start(ctx context.Context, instanceID, poolName string) (string, error) {
	return "", nil
}

// helper function configures and dials the ssh server.
func dial(server, username, password, privatekey string) (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User:            username,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	if privatekey != "" {
		pem := []byte(privatekey)
		signer, err := ssh.ParsePrivateKey(pem)
		if err != nil {
			return nil, err
		}
		config.Auth = append(config.Auth, ssh.PublicKeys(signer))
	}
	if password != "" {
		config.Auth = append(config.Auth, ssh.Password(password))
	}
	return ssh.Dial("tcp", server, config)
}
