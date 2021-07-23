# AWS Runner

This runner provisions EC2 instances in AWS. It also sets up SSH access to the instances, and installs git and docker on the instances. Advanced users can also avail of instance pools to improve build spin up time.

## AWS EC2 prerequisites

There are some pieces of setup that need to be performed on the AWS side first.

+ Set up an access key and access secret [aws secret](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_access-keys.html#Using_CreateAccessKey) which is needed for configuration of the runner.
+ Setup up vpc firewall rules for the build instances [ec2 authorizing-access-to-an-instance](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/authorizing-access-to-an-instance.html) We need allow ingress and egress access to port 22. Once complete you will have a security group id, which is needed for configuration of the runner.
+ (windows only instance) You need to add the `AdministratorAccess` policy to the IAM role associated with the access key and access secret [IAM](https://console.aws.amazon.com/iamv2/home#/users). You will use the instance profile arn `iam_profile_arn`, in your pipeline.
+ Depending on the AMI's you are using, you may need to subscribe to it. We have tested against [Ubuntu 20.04](https://aws.amazon.com/marketplace/pp/prodview-iftkyuwv2sjxi?sr=0-2&ref_=beagle&applicationId=AWSMPContessa) or [Windows 2019 with containers](https://aws.amazon.com/marketplace/pp/prodview-iehgssex6veoi?sr=0-6&ref_=beagle&applicationId=AWSMPContessa).

## Runner setup

Set up the runner by using either an environemtn variables or a `.env` file similar to other Drone runners. Below is a list of the environment variables.

Key | Description
--- | -------------
`DRONE_SETTINGS_AWS_ACCESS_KEY_ID` | AWS access key id, obtained above.
`DRONE_SETTINGS_AWS_ACCESS_KEY_SECRET` | AWS access key secret, obtained above.
`DRONE_SETTINGS_AWS_REGION` | AWS region
`DRONE_SETTINGS_PRIVATE_KEY_FILE` | Private key file for the EC2 instances. You can generate a public and private key using [ssh-keygen](https://ssh.com/ssh/keygen)
`DRONE_SETTINGS_PUBLIC_KEY_FILE`  | Public key file for the EC2 instances
`DRONE_SETTINGS_REUSE_POOL` | Reuse existing ec2 instances on restart of the runner

With an example `.env` file:

```YAML
DRONE_RPC_HOST=localhost:8080
DRONE_RPC_PROTO=http
DRONE_RPC_SECRET=bea26a2221fd8090ea38720fc445eca6
DRONE_SETTINGS_AWS_ACCESS_KEY_ID=XXXXXX
DRONE_SETTINGS_AWS_ACCESS_KEY_SECRET=XXXXX
DRONE_SETTINGS_AWS_REGION=us-east-2
DRONE_SETTINGS_PRIVATE_KEY_FILE=/config/private.key
DRONE_SETTINGS_PUBLIC_KEY_FILE=/config/public.key
```

## `.drone_pool.yml` file

This allows the setup of hot swap pools, where a build does not have to wait for an instance to spin up. For a deeper explanation of how this works, see the [design](https://github.com/drone/proposal/blob/master/design/01-aws-runner.md) documentation.

+ drone_pool.yml is the default file name.
+ Each pool only has one instance type.
+ There can be multiple pools. Different build pipelines can use the same pool
+ You can specify the size of the pool.
+ A pool can only belong to one region.
+ A pipeline can use a pool, or specify its own aws instance configuration.

The name and pools size are entered under the type and kind of build.
Key | Description
--- | -------------
`kind` | is the type of build, it will always be `pipeline`.
`type` | it will always be `aws`.
`name` | is the name of your pool.
`pool_count` | is the number of instances to spawn in the pool.

EC2 Instance information are stored in the instance section:

Key | Description
--- | -------------
`ami` | AMI to use for the instance.
`tags` | Tags to apply to the instance.
`iam_profile_arn` | IAM profile to use for the instance.
`disk` |
  `size` | SIze of the volume in GB.
  `type` | Type of disk to use.
  `iops` | IOPS for the volume.
`network` |
  `vpc` | VPC to use.
  `subnet_id` | Subnet to use.
  `security_groups` | Security groups to use. Array of strings.
  `vpc_security_group_ids` | VPC Security groups to use. Array of strings.
  `private_ip` | Private IP to use.
`device` |
  `name` | Name of the block device.

An example .drone_pool.yml file.

```YAML
kind: pipeline
type: aws
name: common
pool_count: 1

account:
  region: us-east-2

instance:
# ubuntu 18.04 ohio
  ami: ami-051197ce9cbb023ea
  type: t2.nano
  network:
    security_groups:
      - sg-0f5aaeb48d35162a4
```

## Running the drone-runner-aws daemon

With a pool file, env settings.

```BASH
drone-runner-aws config/.env  config/.drone_pool.yml
```

### Complete example using the docker container

Where we have the pool file, env settings and keys in the config folder

```BASH
docker run -it --mount type=bind,source=/home/tp/workspace/drone-runner-aws/config,target=/config --rm   drone/drone-runner-aws daemon /config/.env  /config/.drone_pool.yml
```

## Build files / pipelines

There are some aws specific settings that can be set in the build file, However it shares most of its syntax / functionality with the drone-aws-docker pipelines. For a full example have a look at the `.drone.yml` [here](https://github.com/drone-runners/drone-runner-aws/blob/master/.drone.yml) It uses docker images / docker volumes / drone plugins / services

### Instance information

This is were you can specify the pool to use (recommended) or specify the full instance configuration, this will mean the instance only gets provisioned at build time.

Key | Description
--- | -------------
`use_pool` | the pool to use for that pipeline.

## Basic example of a `.drone.yml` build using a pool

```YAML
kind: pipeline
type: aws
name: ubuntu acceptance tests

instance:
  use_pool: ubuntu

steps:
- name: display go version
  image: golang
  pull: if-not-exists
  commands:
  - go version
```

## Windows build example

You can only run windows containers on a windows instance. NB. you need to add a platform section to the build file.

```YAML
kind: pipeline
type: aws
name: default

platform:
 os: windows

instance:
  use_pool: windows
  tags:
    cat: dog

steps:
  - name: check install
    commands:
      - type C:\ProgramData\Amazon\EC2-Windows\Launch\Log\UserdataExecution.log
```

## Specifying the aws configuation in a .drone.yml build file

This is not recommended way to run, but can be useful in development.

```YAML
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

## Future Improvements

+ tmate integration
+ cli sub command to print ec2 instances information
+ cli sub command to terminate ec2 instances
