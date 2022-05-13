# Commit Checklist

Thank you for creating a pull request! To help us review / merge this can you make sure that your PR adheres as much as possible to the following.

## The Basics

The exec commmand is tested automatically. You will need to run the delegate tests manually, please paste the output of the tests below.

### Testing the delegate command manually

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
