# Scale testing for delegate runner

This utility provide an easy way to scale test runner. Scale test can be done on a specific infra or completely on local using noop driver. Noop driver simulates the vm operations.

How to scale test using noop driver:
1. Use a noop driver in pool.yml:
   `instances:
    - name: test-noop
      default: true
      type: noop
      pool: 20    # total number of warm instances in the pool at all times
      limit: 100  # limit the total number of running servers. If exceeded block or error.
      platform:
        os: linux
        arch: arm64
      spec:
        hibernate: false`
2. Set the following variables: DRONE_LITE_ENGINE_ENABLE_MOCK=true and DRONE_LITE_ENGINE_MOCK_STEP_TIMEOUT_SECS=60. This is to mock out all the lite engine interactions. The step timeout will be how long we wait for each step before continuing.
3. Start the delegate runner using: `go run main.go delegate --pool=pool.yml`
4. Run the scale test on runner using: `go run main.go tester --pool=test-noop --scale=10`. Here pool is the name of the pool on which scale testing will happen and scale is number of parallel builds to run.
