# AWS EC2 setup

+ Setting up access key and access secret [aws secret](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_access-keys.html#Using_CreateAccessKey)
+ Setup vpc firewall rules [ec2 authorizing-access-to-an-instance](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/authorizing-access-to-an-instance.html) to allow access to port 22. (optional you may also want to enable RDP) this will give you a security group id.
+ (windows only) Setup IAM role / ARN look here [ec2 roles](https://console.aws.amazon.com/iam/home#/roles) for more information [iam-roles-for-amazon-ec2.html#ec2-instance-profile](https://docs.aws.amazon.com/AWSEC2/latest/WindowsGuide/iam-roles-for-amazon-ec2.html#ec2-instance-profile) this enables us to run scripts at the start up of windows. You need to add the `AdministratorAccess` policy to the role. You will use the instance profile arn `iam_profile_arn`, in your pipeline.
+ Use config settings env variables for `DRONE_SETTINGS_AWS_ACCESS_KEY_ID`, `DRONE_SETTINGS_AWS_ACCESS_KEY_SECRET` a daemon to enable ping

```BASH
DRONE_RPC_HOST=localhost:8080
DRONE_RPC_PROTO=http
DRONE_RPC_SECRET=bea26a2221fd8090ea38720fc445eca6
DRONE_SETTINGS_AWS_ACCESS_KEY_ID=XXXXXX
DRONE_SETTINGS_AWS_ACCESS_KEY_SECRET=XXXXX
DRONE_SETTINGS_AWS_REGION=us-east-2
```

or you can set these on a per pipeline basis in your .drone.yml ubuntu 19.04 example eg

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
