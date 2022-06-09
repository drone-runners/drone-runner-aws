package amazon

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
)

// config is a struct that implements drivers.Pool interface
type config struct {
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
	tags          map[string]string // user defined tags
	hibernate     bool

	service *ec2.EC2
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}
	// setup service
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
