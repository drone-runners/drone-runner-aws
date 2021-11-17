package cloudaws

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
)

type Credentials struct {
	Client string
	Secret string
	Region string
}

func (prov *Credentials) getClient() *ec2.EC2 {
	const maxRetries = 10

	config := aws.NewConfig()
	config = config.WithRegion(prov.Region)
	config = config.WithMaxRetries(maxRetries)
	config = config.WithCredentials(
		credentials.NewStaticCredentials(prov.Client, prov.Secret, ""),
	)

	mySession := session.Must(session.NewSession())
	return ec2.New(mySession, config)
}
