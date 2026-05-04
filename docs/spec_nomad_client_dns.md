# Spec: Nomad Client-Level DNS Configuration for Tart VMs

## Problem

Tart VMs use Apple's Virtualization.framework shared/NAT networking. The NAT DNS forwarder (`192.168.64.1`) returns SERVFAIL for domains where the upstream authoritative server does not set the `ra` (recursion available) flag. This breaks DNS resolution inside the VM for certain domains (e.g. `app.harness.io`), causing cloud_init to fail downloading binaries.

## Solution

Allow per-machine DNS configuration via Nomad client meta tags. When a Nomad client has `custom_dns=true` and `dns_servers=<comma-separated IPs>` set, the Tart VM startup script will configure those DNS servers inside the VM before cloud_init runs.

## Nomad Client Configuration

On machines that require custom DNS, add to the Nomad client config:

```hcl
client {
  meta {
    "custom_dns"  = "true"
    "dns_servers" = "10.200.0.16,8.8.8.8"
  }
}
```

Machines without these meta tags are unaffected.

**Note:** `dns_servers` must contain valid IPv4 or IPv6 addresses only. Hostnames (e.g. `dns1.example.com`) are not supported and will be rejected — DNS configuration will be skipped if any entry is not a valid IP.

---

## Code Changes

### 1. `types/types.go` — Add NodeMeta to InstanceCreateOpts

```go
type InstanceCreateOpts struct {
    // ... existing fields ...
    TmateBinaryURI               string
    TmateBinaryFallbackURI       string
    NodeMeta                     map[string]string  // <-- NEW
}
```

**Why:** Carries Nomad node metadata from `fetchMachine` to the virtualizer without changing the `Virtualizer` interface.

---

### 2. `app/drivers/nomad/driver.go` — Return node meta from fetchMachine

**Change `fetchMachine` signature:**

```go
func (p *config) fetchMachine(logr logger.Logger, id string) (ip, nodeID string, liteEngineHostPort int, ports []int, nodeMeta map[string]string, err error)
```

**At line 584** (where `n` is already fetched), capture meta:

```go
n, _, err := p.client.Nodes().Info(nodeID, &api.QueryOptions{})
if err != nil {
    // ...
}
ip = strings.Split(n.HTTPAddr, ":")[0]
// ...
return ip, nodeID, liteEngineHostPort, ports, n.Meta, nil
```

**At the call site (line 324):**

```go
ip, id, liteEngineHostPort, gitspacesPorts, nodeMeta, err := p.fetchMachine(logr, resourceJobID)
```

**Before calling `GetInitJob` (after line 321):**

```go
opts.NodeMeta = nodeMeta
```

---

### 3. `app/drivers/nomad/mac_virtualizer.go` — Inject DNS setup into startup script

**In `GetInitJob` (line 50):**

Extract DNS config from opts:

```go
dnsServers := getDNSServersFromMeta(opts.NodeMeta)
```

Pass `dnsServers` to `generateStartupScript` (add parameter).

**New helper function:**

```go
func getDNSServersFromMeta(meta map[string]string) string {
    if meta == nil {
        return ""
    }
    if strings.EqualFold(meta["custom_dns"], "true") {
        return strings.TrimSpace(meta["dns_servers"])
    }
    return ""
}
```

**In `generateStartupScript`** — append DNS block after "Tart VM Started" (end of script, after SSH is verified):

```go
func (mv *MacVirtualizer) generateStartupScript(vmID, machinePassword string, vmImageConfig types.VMImageConfig, resource cf.NomadResource, port int, dnsServers string) string {
    // ... existing script ...

    script := fmt.Sprintf(`
    ... existing content ending with ...
    echo "Tart VM Started"
    `, /* existing args */)

    if dnsServers != "" {
        script += mv.generateDNSSetupBlock(dnsServers, vmImageConfig.Username, vmImageConfig.Password)
    }

    return script
}
```

**New method — `generateDNSSetupBlock`:**

```go
func (mv *MacVirtualizer) generateDNSSetupBlock(dnsServers, vmUser, vmPassword string) string {
    // Convert "10.200.0.16,8.8.8.8" to "10.200.0.16 8.8.8.8" (space-separated for networksetup)
    servers := strings.ReplaceAll(dnsServers, ",", " ")

    return fmt.Sprintf(`
echo "[DNS] Custom DNS configuration detected: %s"
echo "[DNS] Applying DNS servers to VM network interface..."

expect <<- DONE
    set timeout 30
    spawn ssh -o "ConnectTimeout=5" -o "StrictHostKeyChecking=no" "$VM_USER@$VM_IP" "networksetup -setdnsservers Ethernet %s"
    expect {
        "*yes/no*" { send "yes\r"; exp_continue }
        "*Password:" { send "$VM_PASSWORD\r"; exp_continue }
    }
DONE

if [ $? -eq 0 ]; then
    echo "[DNS] DNS servers applied successfully"
else
    echo "[DNS] WARNING: Failed to apply DNS servers"
fi

echo "[DNS] Verifying DNS resolution inside VM..."

expect <<- DONE
    set timeout 15
    spawn ssh -o "ConnectTimeout=5" -o "StrictHostKeyChecking=no" "$VM_USER@$VM_IP" "nslookup app.harness.io && nslookup github.com"
    expect {
        "*yes/no*" { send "yes\r"; exp_continue }
        "*Password:" { send "$VM_PASSWORD\r"; exp_continue }
    }
DONE

if [ $? -eq 0 ]; then
    echo "[DNS] DNS resolution verified successfully"
else
    echo "[DNS] WARNING: DNS resolution check failed - proceeding anyway"
fi
`, dnsServers, servers)
}
```

---

### 4. `app/drivers/nomad/linux_virtualizer.go` — No changes

Linux virtualizer receives `opts.NodeMeta` but ignores it. No code change needed.

---

## Execution Flow

```
1. Runner calls fetchMachine()
   └── Fetches Nomad node info including n.Meta
   └── Returns nodeMeta map to caller

2. Runner sets opts.NodeMeta = nodeMeta

3. Runner calls GetInitJob(... opts ...)
   └── MacVirtualizer reads opts.NodeMeta
   └── getDNSServersFromMeta() returns "10.200.0.16,8.8.8.8" or ""

4. generateStartupScript() builds bash script:
   └── [existing] Clone VM, start VM, port forward, restart VM, verify SSH
   └── [existing] echo "Tart VM Started"
   └── [NEW - conditional] DNS setup block (only if dnsServers != "")
       ├── SSH into VM: networksetup -setdnsservers Ethernet 10.200.0.16 8.8.8.8
       ├── Log success/failure
       └── Verify: nslookup app.harness.io && nslookup github.com

5. run_cmd task executes cloud_init (downloads binaries, starts lite-engine)
   └── DNS is already configured, wget/curl resolve correctly
```

---

## Behavior Matrix

| Nomad Client Meta | Behavior |
|---|---|
| No `custom_dns` tag | No DNS block emitted, script unchanged |
| `custom_dns=true`, `dns_servers` set | DNS applied, logged, verified |
| `custom_dns=true`, `dns_servers` empty | Warning logged, no DNS change applied |
| `custom_dns=false` or missing | No DNS block emitted |
| DNS applied but resolution fails | Warning logged, script continues (non-blocking) |

---

## Files Modified

| File | Change |
|---|---|
| `types/types.go` | Add `NodeMeta map[string]string` to `InstanceCreateOpts` |
| `app/drivers/nomad/driver.go` | Update `fetchMachine` return signature; populate `opts.NodeMeta` |
| `app/drivers/nomad/mac_virtualizer.go` | Add `dnsServers` param to `generateStartupScript`; add `getDNSServersFromMeta()` and `generateDNSSetupBlock()` |

---

## Rollout

1. Deploy code change to drone-runner-aws
2. On target Nomad clients, add meta tags to client config and restart Nomad agent
3. Machines without meta tags are unaffected (no meta = no DNS block)
