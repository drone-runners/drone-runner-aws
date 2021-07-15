# AWS EC2 setup

+ Set up access key and access secret [aws secret](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_access-keys.html#Using_CreateAccessKey)
+ Setup vpc firewall rules [ec2 authorizing-access-to-an-instance](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/authorizing-access-to-an-instance.html) to allow access to port 22. This will give you a security group id, which is needed for configuration.
+ (windows only) Setup IAM role / ARN look here [ec2 roles](https://console.aws.amazon.com/iam/home#/roles) for more information [iam-roles-for-amazon-ec2.html#ec2-instance-profile](https://docs.aws.amazon.com/AWSEC2/latest/WindowsGuide/iam-roles-for-amazon-ec2.html#ec2-instance-profile) this enables us to run scripts at the start up of windows. You need to add the `AdministratorAccess` policy to the role. You will use the instance profile arn `iam_profile_arn`, in your pipeline.
+ Use config settings env variables for `DRONE_SETTINGS_AWS_ACCESS_KEY_ID`, `DRONE_SETTINGS_AWS_ACCESS_KEY_SECRET` a daemon to enable ping
+ Depending on the AMI you are using, you may need to subscribe to it.

## .env file

This sets up the configuration for the runner with the same usual settings with some aws settings.

```BASH
AwsAccessKeyID     string
AwsAccessKeySecret string
AwsRegion          string
PrivateKeyFile     string
PublicKeyFile      string
ReusePool          bool  - on startup shutdowm whether to kill existing instances in EC2
```

With an example setup like :

```BASH
DRONE_RPC_HOST=localhost:8080
DRONE_RPC_PROTO=http
DRONE_RPC_SECRET=bea26a2221fd8090ea38720fc445eca6
DRONE_SETTINGS_AWS_ACCESS_KEY_ID=XXXXXX
DRONE_SETTINGS_AWS_ACCESS_KEY_SECRET=XXXXX
DRONE_SETTINGS_AWS_REGION=us-east-2
```

## .drone_pool.yml file

This is how to setup pools.

+ drone_pool.yml is the default file name.
+ Each pool only has one instance type.
+ There can be multiple pools. Different build pipelines can use the same pool
+ You can specify the size of the pool.
+ A pool can only belong to one region.
+ A pipeline can use a pool, or specify its own aws instance configuration.

The name and pools size are entered under the type and kind of build.
EC2 Instance information are stored in the instance section:

```BASH
Image         string
Name          string
Region        string
Size          string
Subnet        string
Groups        []string
Device        string
PrivateIP     bool
VolumeType    string
VolumeSize    int64
VolumeIops    int64
IamProfileArn string
Userdata      string
Tags          map[string]string
```

An example .drone_pool.yml file.

```BASH
kind: pipeline
type: aws
name: common
pool_count: 1

account:
  region: us-east-2

instance:
# ubuntu 18.04 t1-micro ohio
  ami: ami-051197ce9cbb023ea
  type: t2.nano
  network:
    security_groups:
      - sg-0f5aaeb48d35162a4
```

### Specifying the aws configuation in a .drone.yml build file

```BASH
kind: pipeline
type: aws
name: default

account:
  access_key_id: XXXXXX
  secret_access_key: XXXXXX
  region: us-east-2

instance:
  ami: ami-051197ce9cbb023ea
  key_pair: test_key_pair # this sets up the instance with an AWS key pair as well
  network:
    security_groups:
      - sg-5d255b29 # security group id

steps:
- name: build
  commands:
  - uname -a
```

And here is a windows example, NB we set the platform.os to `windows` and we set the instance.iam_profile_arn

```BASH
kind: pipeline
type: aws
name: default

account:
  access_key_id: XXXXXX
  secret_access_key: XXXXXX
  region: us-east-2

platform:
  os: windows

instance:
  ami: ami-0b697c4ae566cad55
  iam_profile_arn: "arn:aws:iam::577992088676:instance-profile/drone_iam_role"
  key_pair: test_key_pair
  type: t2.nano
  network:
    security_groups:
      - sg-5d255b29

steps:
- name: build
  commands:
  - date

- name: test
  commands:
  - echo "hello world"
```

## Complete example using the binary

With a pool file, env settings.

```BASH
drone-runner-aws config/.env  config/.drone_pool.yml
```

## Complete example using the docker container

Where we have the pool file, env settings and keys in the config folder

```BASH
docker run -it --mount type=bind,source=/home/tp/workspace/drone-runner-aws/config,target=/config --rm   drone/drone-runner-aws daemon /config/.env  /config/.drone_pool.yml
```
