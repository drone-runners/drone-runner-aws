Sample pool file for nomad:

version: "1"
instances:
- name: linux-amd64-bare-metal
  type: nomad
  pool: 0  # total number of warm instances in the pool at all times
  limit: 0
  platform:
    os: linux
    arch: amd64
  spec:
    server:
      address: <>
      client_key_path: <>
      client_cert_path: <>
      ca_cert_path: <>
    vm:
      image: harness/vmimage:v1
      cpus: "2"
      mem_gb: "2"
      noop: true # if you want to skip VM creation


To enable scale testing, set the following variables as well to mock out lite engine and VM interactions:
DRONE_LITE_ENGINE_ENABLE_MOCK=true
DRONE_LITE_ENGINE_MOCK_STEP_TIMEOUT_SECS=60.

On setting these, nomad would just submit dummy jobs but not create any actual VMs.
