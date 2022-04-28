package amazon

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
)

// provider is a struct that implements drivers.Pool interface
type provider struct {
	spotInstance     bool
	region           string
	availabilityZone string
	retries          int

	accessKeyID     string
	secretAccessKey string
	keyPairName     string

	rootDir string

	image         string
	size          string
	sizeAlt       string
	user          string
	userData      string
	subnet        string
	vpc           string
	groups        []string
	allocPublicIP bool
	volumeType    string
	volumeSize    int64
	volumeIops    int64
	deviceName    string
	iamProfileArn string
	hibernate     bool

	service *ec2.EC2
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(provider)
	for _, opt := range opts {
		opt(p)
	}
	if p.retries == 0 {
		p.retries = 10
	}
	if p.region == "" {
		p.region = "us-east-1"
		if p.availabilityZone == "" {
			p.availabilityZone = "us-east-1a"
		}
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
