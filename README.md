# AWS Runner

[![Build Status](https://harness.drone.io/api/badges/drone-runners/drone-runner-aws/status.svg)](https://harness.drone.io/drone-runners/drone-runner-aws)

This runner provisions EC2 instances in AWS for both windows and Linux. It also sets up SSH access and installs git. The installation of Docker on the instances allows the running of the build in Hybrid mode: where Drone Plugins can run or build steps in container along with build steps on the instance operating system. Pools of hot swappable EC2 instances are created on startup of the runner to improve build spin up time.

## Installation

For more information about installing this runner look at the [installation documentation](https://docs.drone.io/runner/vm/overview/).

## Configuration

For more information about configuring this runner look at the [configuration documentation](https://docs.drone.io/runner/vm/configuration/).

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
+ add the ngrok url to the env file `DRONE_SETTINGS_LITE_ENGINE_PATH=https://c6bf-80-7-0-64.ngrok.io`

## Testing the delegate command

+ Run the delegate command, wait for the pool creation to complete.
+ setup an instance:

```BASH
curl -d '{"id": "unique-stage-id","correlation_id":"abc1","pool_id":"ubuntu", "setup_request": {"network": {"id":"drone"}, "platform": { "os":"ubuntu" }}}' -H "Content-Type: application/json" -X POST  http://127.0.0.1:3000/setup
```

+ run a step on the instance:

```BASH
curl -d '{"id":"unique-stage-id","pool_id":"ubuntu","instance_id":"<INSTANCE ID>","correlation_id":"xyz2", "start_step_request":{"id":"step4", "image": "alpine:3.11", "working_dir":"/tmp", "run":{"commands":["sleep 30"], "entrypoint":["sh", "-c"]}}}' -H "Content-Type: application/json" -X POST  http://127.0.0.1:3000/step
```

or, directly by IP address returned by the setup API call:

```BASH
curl -d '{"ip_address":"<IP OF INSTANCE>","pool_id":"ubuntu","correlation_id":"xyz2", "start_step_request":{"id":"step4", "image": "alpine:3.11", "working_dir":"/tmp", "run":{"commands":["sleep 30"], "entrypoint":["sh", "-c"]}}}' -H "Content-Type: application/json" -X POST  http://127.0.0.1:3000/step
```

+ destroy an instance:

```BASH
curl -d '{"id":"unique-stage-id","instance_id":"<INSTANCE ID>","pool_id":"ubuntu","correlation_id":"uvw3"}' -H "Content-Type: application/json" -X POST  http://127.0.0.1:3000/destroy
```
