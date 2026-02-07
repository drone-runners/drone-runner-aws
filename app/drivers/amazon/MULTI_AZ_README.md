# Multi-AZ Round Robin for AWS

This document explains the Multi-Availability Zone (Multi-AZ) round-robin approach implemented in the AWS driver for distributing VM instances across multiple availability zones.

## Overview

The Multi-AZ round-robin feature allows you to configure multiple availability zones with their corresponding subnets. The driver will distribute VM instances across these zones in a round-robin fashion, providing:

- **High Availability**: Workloads are spread across multiple AZs, reducing the impact of zone-specific outages
- **Load Distribution**: Even distribution of instances across zones helps avoid capacity issues in any single zone
- **Automatic Failover**: If one zone experiences issues, subsequent instances are placed in other zones

## How It Works

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           VM Creation Request                                │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
                    ┌─────────────────────────────────┐
                    │   Is specific zone requested?   │
                    └─────────────────────────────────┘
                           │                │
                          Yes               No
                           │                │
                           ▼                ▼
              ┌────────────────────┐   ┌────────────────────────────┐
              │ Use requested zone │   │ Are zone_details defined?  │
              │ + matching subnet  │   └────────────────────────────┘
              └────────────────────┘           │              │
                                              Yes             No
                                               │              │
                                               ▼              ▼
                                  ┌─────────────────────┐  ┌──────────────────┐
                                  │ Round-Robin Select  │  │ Use pool default │
                                  │ from zone_details   │  │ AZ and subnet    │
                                  └─────────────────────┘  └──────────────────┘
                                               │
                                               ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                         Round-Robin Selection                                │
│                                                                              │
│   zone_details:                                                              │
│   ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐             │
│   │   Zone 0        │  │   Zone 1        │  │   Zone 2        │             │
│   │   us-west-1a    │  │   us-west-1b    │  │   us-west-1c    │             │
│   │   subnet-aaa    │  │   subnet-bbb    │  │   subnet-ccc    │             │
│   └─────────────────┘  └─────────────────┘  └─────────────────┘             │
│          ▲                    ▲                    ▲                         │
│          │                    │                    │                         │
│     Request 1            Request 2            Request 3                      │
│     Request 4            Request 5            Request 6                      │
│     Request 7            ...                  ...                            │
│                                                                              │
│   Index cycles: 0 → 1 → 2 → 0 → 1 → 2 → ...                                 │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Thread Safety

The round-robin implementation uses atomic operations to ensure thread-safe zone selection:

1. **Atomic Compare-And-Swap (CAS)**: Ensures only one goroutine updates the index at a time
2. **Bounded Counter**: The index cycles within `[0, numZones)` to prevent overflow
3. **Timeout Fallback**: If CAS contention persists for 10+ seconds, falls back to random selection

```
┌─────────────────────────────────────────────────────────────────┐
│                    Concurrent Request Handling                   │
│                                                                  │
│   Request A ──┐                                                  │
│               │     ┌──────────────────────┐                     │
│   Request B ──┼────▶│  Atomic CAS Loop     │────▶ Zone Selected  │
│               │     │  (Thread-Safe)       │                     │
│   Request C ──┘     └──────────────────────┘                     │
│                              │                                   │
│                              ▼                                   │
│                     ┌──────────────────────┐                     │
│                     │  > 10s contention?   │                     │
│                     └──────────────────────┘                     │
│                        │              │                          │
│                       No             Yes                         │
│                        │              │                          │
│                        ▼              ▼                          │
│                   Continue      Random Fallback                  │
│                   CAS Loop      Selection                        │
└─────────────────────────────────────────────────────────────────┘
```

## Configuration

### Sample Pool YAML

```yaml
version: "1"
instances:
  - name: linux-amd64-aws
    default: true
    type: amazon
    pool: 0
    limit: 10
    platform:
      os: linux
      arch: amd64
      os_name: amazon-linux
    spec:
      account:
        region: us-west-1
        key_pair_name: xxxx-keypair
        # availability_zone: us-west-1a  # Deprecated - use network.zone_details instead
      ami: ami-xxxxxxxxxxxxxxxxx
      size: t3.medium
      hibernate: false
      disk:
        size: 100
        type: gp3
      network:
        private_ip: false
        security_groups:
          - sg-xxxxxxxxxxxxxxxxx
        # Multi-AZ configuration
        zone_details:
          - availability_zone: us-west-1a
            subnet_id: subnet-xxxxxxxxxxxxxxxxx
          - availability_zone: us-west-1b
            subnet_id: subnet-yyyyyyyyyyyyyyyyy
          - availability_zone: us-west-1c
            subnet_id: subnet-zzzzzzzzzzzzzzzzz
```

### Configuration Fields

| Field | Location | Description |
|-------|----------|-------------|
| `zone_details` | `spec.network.zone_details` | **Recommended**: List of AZ and subnet pairs for round-robin |
| `zone_details` | `spec.zone_details` | **Deprecated**: Legacy location, use `network.zone_details` |
| `availability_zone` | `spec.account.availability_zone` | **Deprecated**: Single zone, use `zone_details` for multi-AZ |
| `subnet_id` | `spec.network.subnet_id` | **Deprecated**: Single subnet, use `zone_details` for multi-AZ |

### Priority Order

The driver selects the availability zone and subnet in this order:

1. **Request-specified zone**: If the pipeline/request specifies a zone, use it (and find matching subnet from `zone_details`)
2. **Round-robin from zone_details**: If `network.zone_details` is configured, cycle through them
3. **Legacy zone_details**: If `spec.zone_details` is configured (deprecated), use it as fallback
4. **Pool defaults**: Use the static `availability_zone` and `subnet_id` from pool config

## Benefits

| Benefit | Description |
|---------|-------------|
| **Fault Tolerance** | If `us-west-1a` has an outage, instances continue launching in `us-west-1b` and `us-west-1c` |
| **Capacity Distribution** | Avoids exhausting instance capacity in a single zone |
| **Network Isolation** | Each AZ can have its own subnet with specific routing rules |
| **Cost Optimization** | Can leverage zone-specific pricing for spot instances |

## Migration Guide

If you're currently using the deprecated single-zone configuration:

**Before (Deprecated):**
```yaml
spec:
  account:
    region: us-west-1
    availability_zone: us-west-1a   # Deprecated
  network:
    subnet_id: subnet-aaa           # Deprecated
    security_groups:
      - sg-xxx
```

**After (Recommended):**
```yaml
spec:
  account:
    region: us-west-1
  network:
    security_groups:
      - sg-xxx
    zone_details:
      - availability_zone: us-west-1a
        subnet_id: subnet-aaa
      - availability_zone: us-west-1b
        subnet_id: subnet-bbb
```
