module github.com/drone-runners/drone-runner-aws

go 1.17

replace github.com/docker/docker => github.com/docker/engine v17.12.0-ce-rc1.0.20200309214505-aa6a9891b09c+incompatible

require (
	github.com/aws/aws-sdk-go v1.42.10
	github.com/buildkite/yaml v2.1.0+incompatible
	github.com/dchest/uniuri v0.0.0-20160212164326-8902c56451e9
	github.com/drone/drone-go v1.7.1
	github.com/drone/envsubst v1.0.3
	github.com/drone/runner-go v1.12.0
	github.com/drone/signal v1.0.0
	github.com/ghodss/yaml v1.0.0
	github.com/go-chi/chi/v5 v5.0.7
	github.com/google/go-cmp v0.5.5
	github.com/harness/lite-engine v0.0.2-0.20220209135420-984164d12eac
	github.com/joho/godotenv v1.4.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/mattn/go-isatty v0.0.14
	github.com/sirupsen/logrus v1.8.1
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	gopkg.in/yaml.v2 v2.4.0
)

require (
	github.com/99designs/basicauth-go v0.0.0-20160802081356-2a93ba0f464d // indirect
	github.com/99designs/httpsignatures-go v0.0.0-20170731043157-88528bf4ca7e // indirect
	github.com/Microsoft/go-winio v0.4.11 // indirect
	github.com/alecthomas/template v0.0.0-20190718012654-fb15b899a751 // indirect
	github.com/alecthomas/units v0.0.0-20210927113745-59d0afb8317a // indirect
	github.com/andybalholm/brotli v1.0.1 // indirect
	github.com/bmatcuk/doublestar v1.1.1 // indirect
	github.com/cenkalti/backoff/v4 v4.1.1 // indirect
	github.com/containerd/containerd v1.4.12 // indirect
	github.com/coreos/go-semver v0.3.0 // indirect
	github.com/docker/distribution v2.7.1+incompatible // indirect
	github.com/docker/docker v0.0.1 // indirect
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.4.0 // indirect
	github.com/dsnet/compress v0.0.2-0.20210315054119-f66993602bf5 // indirect
	github.com/go-chi/chi v1.5.4 // indirect
	github.com/gofrs/uuid v4.0.0+incompatible // indirect
	github.com/gogo/protobuf v0.0.0-20170307180453-100ba4e88506 // indirect
	github.com/golang/mock v1.4.4 // indirect
	github.com/golang/protobuf v1.3.3 // indirect
	github.com/golang/snappy v0.0.2 // indirect
	github.com/hashicorp/errwrap v1.0.0 // indirect
	github.com/hashicorp/go-multierror v1.0.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/klauspost/compress v1.11.4 // indirect
	github.com/klauspost/pgzip v1.2.5 // indirect
	github.com/kr/pretty v0.2.0 // indirect
	github.com/linkedin/goavro/v2 v2.10.1 // indirect
	github.com/mattn/go-zglob v0.0.3 // indirect
	github.com/mholt/archiver/v3 v3.5.1 // indirect
	github.com/natessilva/dag v0.0.0-20180124060714-7194b8dcc5c4 // indirect
	github.com/nwaples/rardecode v1.1.0 // indirect
	github.com/opencontainers/go-digest v1.0.0-rc1 // indirect
	github.com/opencontainers/image-spec v1.0.2 // indirect
	github.com/pierrec/lz4/v4 v4.1.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/stretchr/testify v1.6.1 // indirect
	github.com/ulikunitz/xz v0.5.9 // indirect
	github.com/xi2/xz v0.0.0-20171230120015-48954b6210f8 // indirect
	golang.org/x/crypto v0.0.0-20201221181555-eec23a3978ad // indirect
	golang.org/x/net v0.0.0-20210805182204-aaa1db679c0d // indirect
	golang.org/x/sys v0.0.0-20211124211545-fe61309f8881 // indirect
	golang.org/x/text v0.3.6 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	google.golang.org/genproto v0.0.0-20190819201941-24fa4b261c55 // indirect
	google.golang.org/grpc v1.29.1 // indirect
)
