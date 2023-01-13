# Scale testing for delegate runner

This utility provide an easy way to scale test runner. Scale test can be done on a specific infra or completely on local using noop driver. Noop driver simulates the vm operations.

How to scale test using noop driver:
1. Start the lite-engine on local in insecure mode using using: `SERVER_INSECURE=true go run main.go server`
2. Update the endpoint of lite-engine manually to http from https in checkInstanceConnectivity (manager.go file) & GetClient (lehelper.go file). This is required for runner to connect to lite-engine on local.
3. Use a noop driver in pool.yml:
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
4. Start the delegate runner using: `go run main.go delegate --pool=pool.yml`
5. Run the scale test on runner using: `go run main.go tester --pool=test-noop --scale=10`. Here pool is the name of the pool on which scale testing will happen and scale is number of parallel builds to run.