module github.com/drone-runners/drone-runner-aws

go 1.17

replace github.com/docker/docker => github.com/docker/engine v17.12.0-ce-rc1.0.20200309214505-aa6a9891b09c+incompatible

require (
	github.com/aws/aws-sdk-go v1.31.7
	github.com/buildkite/yaml v2.1.0+incompatible
	github.com/cenkalti/backoff/v3 v3.2.2
	github.com/dchest/uniuri v0.0.0-20160212164326-8902c56451e9
	github.com/drone/drone-go v1.6.0
	github.com/drone/envsubst v1.0.3
	github.com/drone/runner-go v1.9.0
	github.com/drone/signal v1.0.0
	github.com/ghodss/yaml v1.0.0
	github.com/google/go-cmp v0.3.0
	github.com/gosimple/slug v1.9.0
	github.com/harness/lite-engine v0.0.0-20211122121913-882c4552ef5b
	github.com/joho/godotenv v1.4.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/mattn/go-isatty v0.0.8
	github.com/pkg/errors v0.9.1
	github.com/pkg/sftp v1.13.0
	github.com/sirupsen/logrus v1.8.1
	golang.org/x/crypto v0.0.0-20201221181555-eec23a3978ad
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	gopkg.in/yaml.v2 v2.2.2
)

require (
	github.com/99designs/basicauth-go v0.0.0-20160802081356-2a93ba0f464d // indirect
	github.com/99designs/httpsignatures-go v0.0.0-20170731043157-88528bf4ca7e // indirect
	github.com/Microsoft/go-winio v0.4.11 // indirect
	github.com/alecthomas/template v0.0.0-20190718012654-fb15b899a751 // indirect
	github.com/alecthomas/units v0.0.0-20210927113745-59d0afb8317a // indirect
	github.com/bmatcuk/doublestar v1.1.1 // indirect
	github.com/cenkalti/backoff/v4 v4.1.1 // indirect
	github.com/containerd/containerd v1.3.4 // indirect
	github.com/coreos/go-semver v0.3.0 // indirect
	github.com/docker/distribution v2.7.1+incompatible // indirect
	github.com/docker/docker v0.0.1 // indirect
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.4.0 // indirect
	github.com/go-chi/chi v1.5.4 // indirect
	github.com/gofrs/uuid v4.0.0+incompatible // indirect
	github.com/gogo/protobuf v0.0.0-20170307180453-100ba4e88506 // indirect
	github.com/golang/protobuf v1.3.3 // indirect
	github.com/hashicorp/errwrap v1.0.0 // indirect
	github.com/hashicorp/go-multierror v1.0.0 // indirect
	github.com/jmespath/go-jmespath v0.3.0 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/kr/pretty v0.2.0 // indirect
	github.com/mattn/go-zglob v0.0.3 // indirect
	github.com/natessilva/dag v0.0.0-20180124060714-7194b8dcc5c4 // indirect
	github.com/opencontainers/go-digest v1.0.0-rc1 // indirect
	github.com/opencontainers/image-spec v1.0.1 // indirect
	github.com/rainycape/unidecode v0.0.0-20150907023854-cb7f23ec59be // indirect
	golang.org/x/net v0.0.0-20200202094626-16171245cfb2 // indirect
	golang.org/x/sys v0.0.0-20210119212857-b64e53b001e4 // indirect
	golang.org/x/text v0.3.0 // indirect
	google.golang.org/genproto v0.0.0-20190819201941-24fa4b261c55 // indirect
	google.golang.org/grpc v1.29.1 // indirect
)
