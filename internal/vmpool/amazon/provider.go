package amazon

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone-runners/drone-runner-aws/oshelp"
)

// provider is a struct that implements vmpool.Pool interface
type provider struct {
	name       string
	runnerName string

	spotInstance     bool
	region           string
	availabilityZone string
	retries          int

	accessKeyID     string
	secretAccessKey string
	keyPairName     string

	os      string
	arch    string
	rootDir string

	image         string
	size          string
	sizeAlt       string
	user          string
	userData      string
	subnet        string
	groups        []string
	allocPublicIP bool
	volumeType    string
	volumeSize    int64
	volumeIops    int64
	tags          map[string]string
	deviceName    string
	iamProfileArn string

	// pool size data
	pool  int
	limit int

	service *ec2.EC2
}

func New(opts ...Option) (vmpool.Pool, error) {
	p := new(provider)
	for _, opt := range opts {
		opt(p)
	}
	if p.retries == 0 {
		p.retries = 10
	}
	if p.pool < 0 {
		p.pool = 0
	}
	if p.limit <= 0 {
		p.limit = 100
	}
	if p.pool > p.limit {
		p.limit = p.pool
	}
	if p.region == "" {
		p.region = "us-east-1"
		if p.availabilityZone == "" {
			p.availabilityZone = "us-east-1a"
		}
	}
	if p.os == "" {
		p.os = oshelp.OSLinux
	}
	if p.arch == "" {
		p.arch = "amd64"
	}
	// set default instance type if not provided
	if p.size == "" {
		if p.arch == "arm64" {
			p.size = "a1.medium"
		} else {
			p.size = "t3.nano"
		}
	}
	// put something into tags even if empty
	if p.tags == nil {
		p.tags = make(map[string]string)
	}
	// set the default disk size if not provided
	if p.volumeSize == 0 {
		p.volumeSize = 32
	}
	// set the default disk type if not provided
	if p.volumeType == "" {
		p.volumeType = "gp2"
	}
	// set the default iops
	if p.volumeType == "io1" && p.volumeIops == 0 {
		p.volumeIops = 100
	}
	// set the default device
	if p.deviceName == "" {
		p.deviceName = "/dev/sda1"
	}
	// set the default ssh user. this user account is responsible for executing the pipeline script.
	if p.user == "" {
		if p.os == oshelp.OSWindows {
			p.user = "Administrator"
		} else {
			p.user = "root"
		}
	}
	// setup service if not provided
	if p.service == nil {
		config := &aws.Config{
			Region:     aws.String(p.region),
			MaxRetries: aws.Int(p.retries),
		}
		if p.accessKeyID != "" && p.secretAccessKey != "" {
			config.Credentials = credentials.NewStaticCredentials(p.accessKeyID, p.secretAccessKey, "")
		}
		mySession := session.Must(session.NewSession())
		p.service = ec2.New(mySession, config)
	}
	return p, nil
}
