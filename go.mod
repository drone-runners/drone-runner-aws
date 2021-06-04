module github.com/drone-runners/drone-runner-aws

go 1.12

require (
	github.com/alecthomas/template v0.0.0-20190718012654-fb15b899a751 // indirect
	github.com/alecthomas/units v0.0.0-20190924025748-f65c72e2690d // indirect
	github.com/aws/aws-sdk-go v1.31.7
	github.com/buildkite/yaml v2.1.0+incompatible
	github.com/dchest/uniuri v0.0.0-20160212164326-8902c56451e9
	github.com/drone/drone-go v1.2.1-0.20200326064413-195394da1018
	github.com/drone/envsubst v1.0.2
	github.com/drone/runner-go v1.6.1-0.20200415215637-a82f0982f1be
	github.com/drone/signal v1.0.0
	github.com/ghodss/yaml v1.0.0
	github.com/google/go-cmp v0.3.0
	github.com/gosimple/slug v1.9.0
	github.com/joho/godotenv v1.3.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/kr/pretty v0.2.0
	github.com/mattn/go-isatty v0.0.8
	github.com/pkg/sftp v1.13.0
	github.com/sirupsen/logrus v1.4.2
	golang.org/x/crypto v0.0.0-20201221181555-eec23a3978ad
	golang.org/x/sync v0.0.0-20190423024810-112230192c58
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
)
