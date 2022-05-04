# Commit Checklist

Thank you for creating a pull request! To help us review / merge this can you make sure that your PR adheres as much as possible to the following.

## The Basics

Please show screen grabs of running the accepance tests. Testing is currently manual, but we will be adding automated testing soon.

### Testing a drone build command

- Tested the drone runner by running:

```bash
drone exec ?? more to come soon ...
```

### Testing the delegate command

```BASH
curl -d '{"id": "unique-stage-id","correlation_id":"abc1","pool_id":"ubuntu", "setup_request": {"network": {"id":"drone"}, "platform": { "os":"ubuntu" }}}' -H "Content-Type: application/json" -X POST  http://127.0.0.1:3000/setup
```

- run a step on the instance:

```BASH
curl -d '{"id":"unique-stage-id","pool_id":"ubuntu","instance_id":"<INSTANCE ID>","correlation_id":"xyz2", "start_step_request":{"id":"step4", "image": "alpine:3.11", "working_dir":"/tmp", "run":{"commands":["sleep 30"], "entrypoint":["sh", "-c"]}}}' -H "Content-Type: application/json" -X POST  http://127.0.0.1:3000/step
```

or, directly by IP address returned by the setup API call:

```BASH
curl -d '{"ip_address":"<IP OF INSTANCE>","pool_id":"ubuntu","correlation_id":"xyz2", "start_step_request":{"id":"step4", "image": "alpine:3.11", "working_dir":"/tmp", "run":{"commands":["sleep 30"], "entrypoint":["sh", "-c"]}}}' -H "Content-Type: application/json" -X POST  http://127.0.0.1:3000/step
```

- destroy an instance:

```BASH
curl -d '{"id":"unique-stage-id","instance_id":"<INSTANCE ID>","pool_id":"ubuntu","correlation_id":"uvw3"}' -H "Content-Type: application/json" -X POST  http://127.0.0.1:3000/destroy
```
