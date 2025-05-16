# AWS Runner

[![Build Status](https://harness.drone.io/api/badges/drone-runners/drone-runner-aws/status.svg)](https://harness.drone.io/drone-runners/drone-runner-aws)

This runner provisions instances in various clouds for both mac, windows and Linux. It also sets up the lite-engine and installs git. The installation of Docker on the instances allows the running of the build in Hybrid mode: where Drone Plugins can run or build steps in container along with build steps on the instance operating system. Pools of hot swappable EC2 instances are created on startup of the runner to improve build spin up time.

## Installation

For more information about installing this runner look at the [installation documentation](https://docs.drone.io/runner/vm/overview/).

## Configuration

For more information about configuring this runner look at the [configuration documentation](https://docs.drone.io/runner/vm/configuration/).

## High Availablity
We can deploy multiple replicas of runner to ensure high availablity. Below is an example of a deployment yaml that deploys 2 replicas of the runner behind a load balancer.
<pre>---
apiVersion: v1
kind: Namespace
metadata:
  name: harness-delegate-ng
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: drone-runner
  namespace: harness-delegate-ng
spec:
  replicas: 2
  selector:
    matchLabels:
      app: drone-runner
  template:
    metadata:
      labels:
        app: drone-runner
    spec:
      containers:
        - name: drone-runner
          image: drone/drone-runner-aws:1.0.0-rc.187
          args: ["delegate", "--pool", "/runner/gcp-pool.yml"]
          env:
            - name: DRONE_RUNNER_HA
              value: "true"
            - name: DRONE_DATABASE_DRIVER
              value: postgres
            - name: DRONE_DATABASE_DATASOURCE
              value: host=runnerha-postgres-service.harness-delegate-ng.svc.cluster.local port=5431 user=admin password=password dbname=dlite sslmode=disable
          ports:
            - containerPort: 3000
          volumeMounts:
            - name: config-volume
              mountPath: /runner
              readOnly: true
      volumes:
        - name: config-volume
          projected:
            sources:
              - configMap:
                  name: drone-runner-config
              - secret:
                  name: drone-runner-secret
---
apiVersion: v1
kind: Service
metadata:
  name: drone-runner-lb
  namespace: harness-delegate-ng
spec:
  type: LoadBalancer
  selector:
    app: drone-runner
  ports:
    - port: 3000
      targetPort: 3000
      protocol: TCP</pre>

The load balancer ip obtained above can be used in delegate with env variable RUNNER_URL. 
Populate DRONE_DATABASE_DATASOURCE accordingly. Note that above yaml mounts a volume config-volume with path /runner. Make sure your gcp-pool.yml and secrets required are mapped properly.
Here is the gcp-pool.yaml used in above example

<pre>version: "1"
instances:
  - name: linux-amd64
    type: google
    pool: 2
    limit: 10
    platform:
      os: linux
      arch: amd64 
    spec:
      hibernate: false
      privateIP: true
      account:
        project_id: projectname
        json_path: runner/gcp-secret.json
      image: projects/projectname/global/images/hosted-vm-64
      machine_type: e2-medium
      zones:
        - us-central1-a
      disk:
        size: 100</pre>


## Creating a build pipelines

For more information about creating a build pipeline look at the [pipeline documentation](https://docs.drone.io/pipeline/aws/overview/).

## Design

This runner was initially designed in the following [proposal](https://github.com/drone/proposal/blob/master/design/01-aws-runner.md).

## Release procedure

**MAKE SURE THE BUILD STATUS IS GREEN BEFORE YOU RELEASE A NEW VERSION**

### Build and test the mac binary version

1. Build the mac binary version of the runner.

    ```bash
    CGO_ENABLED=1 go build -o drone-runner-aws-darwin-amd64
    ```

2. Run the exec command to test the runner. The pool name must be `macpool`.

    ```bash
    drone-runner-aws-darwin-amd64 exec test_files/compiler/drone_mac.yml --pool pool.yml --debug --trace --repo-http='https://github.com/tphoney/bash_plugin' --repo-branch='main' --commit-target='main' --commit-after='7e5f437589cdf071769158ce219b2f443ca13074'
    ```

### Run the changelog generator

```BASH
docker run -it --rm -v "$(pwd)":/usr/local/src/your-app githubchangeloggenerator/github-changelog-generator -u drone-runners -p drone-runner-aws -t <secret github token>
```

You can generate a token by logging into your GitHub account and going to Settings -> Personal access tokens.

Next we tag the PR's with the fixes or enhancements labels. If the PR does not fufil the requirements, do not add a label.

Run the changelog generator again with the future version according to semver.

```BASH
docker run -it --rm -v "$(pwd)":/usr/local/src/your-app githubchangeloggenerator/github-changelog-generator -u drone-runners -p drone-runner-aws -t <secret token> --future-release v1.0.0
```

Create your pull request for the release. Get it merged then tag the release.

## Testing against a custom lite engine

+ build the lite-engine
+ host the lite-engine binary `python3 -m http.server`
+ run ngrok to expose the webserver `ngrok http 8000`
+ add the ngrok url to the env file `DRONE_LITE_ENGINE_PATH=https://c6bf-80-7-0-64.ngrok.io`

## Testing the delegate command

+ Run the delegate command, wait for the pool creation to complete.
+ setup an instance:

```BASH
curl -d '{"id": "unique-stage-id","correlation_id":"abc1","pool_id":"ubuntu", "setup_request": {"network": {"id":"drone"}, "platform": { "os":"ubuntu" }}}' -H "Content-Type: application/json" -X POST  http://127.0.0.1:3000/setup
```

+ run a step on the instance:

```BASH
curl -d '{"stage_runtime_id":"unique-stage-id","pool_id":"ubuntu","instance_id":"<INSTANCE ID>","correlation_id":"xyz2", "start_step_request":{"id":"step4", "image": "alpine:3.11", "working_dir":"/tmp", "run":{"commands":["sleep 30"], "entrypoint":["sh", "-c"]}}}' -H "Content-Type: application/json" -X POST  http://127.0.0.1:3000/step
```

or, directly by IP address returned by the setup API call:

```BASH
curl -d '{"ip_address":"<IP OF INSTANCE>","pool_id":"ubuntu","correlation_id":"xyz2", "start_step_request":{"id":"step4", "image": "alpine:3.11", "working_dir":"/tmp", "run":{"commands":["sleep 30"], "entrypoint":["sh", "-c"]}}}' -H "Content-Type: application/json" -X POST  http://127.0.0.1:3000/step
```

+ destroy an instance:

```BASH
curl -d '{"stage_runtime_id":"unique-stage-id","instance_id":"<INSTANCE ID>","pool_id":"ubuntu","correlation_id":"uvw3"}' -H "Content-Type: application/json" -X POST  http://127.0.0.1:3000/destroy
```

## Testing the runner in delegate-less mode

The AWS runner can also connect to the Harness platform where it functions as both a task receiver and executor. In the delegate mode, the task receiving is done by the java delegate process. In the delegate-less mode, the task receiving is done by the same runner process.

The delegate-less mode does not start an API server, instead it polls the manager periodically to receive any tasks generated by the Harness platform. Hence, it is best to test this mode directly with Harness.

Steps:
+ Create a .env file with variables needed to connect with the Harness platform.

```
DLITE_ACCOUNT_ID=<account-id>
DLITE_ACCOUNT_SECRET=<account-secret>
DLITE_MANAGER_ENDPOINT=<manager-endpoint>
DLITE_NAME=<name-of-runner>
```

The values of the above variables can be found by logging into a Harness account and adding a delegate. The spec for the delegate would contain the account ID, the secret (DELEGATE_TOKEN), and the endpoint (MANAGER_HOST_AND_PORT)

+ Run the runner in delegate-less mode `go run main.go dlite --pool=pool.yml`

If the logs say the runner has been registered successfully, you should be able to see the runner in the delegates screen on Harness.

+ Create a pipeline and execute it. Since this is in beta at the moment, a UI does not exist on Harness for it. To be able to leverage this runner, remove the infrastructure part in the pipeline and add a field `runsOn: <pool-name>` at the same level as `execution:` (directly under `spec`)

+ You should see logs in the runner corresponding to the created tasks.
