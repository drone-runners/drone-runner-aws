package cloudaws

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/sirupsen/logrus"
)

type Credentials struct {
	Client string
	Secret string
	Region string
}

func (prov *Credentials) getClient() *ec2.EC2 {
	const maxRetries = 10

	config := &aws.Config{
		Region:     aws.String(prov.Region),
		MaxRetries: aws.Int(maxRetries),
	}
	if prov.Client != "" && prov.Secret != "" {
		config.Credentials = credentials.NewStaticCredentials(prov.Client, prov.Secret, "")
	} else {
		logrus.Debugf("AWS Key and/or Secret not provided (falling back to ec2 instance profile)")
	}

	mySession := session.Must(session.NewSession())
	return ec2.New(mySession, config)
}
