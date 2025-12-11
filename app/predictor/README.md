# EMA Weekend Decay Predictor

This predictor forecasts how many cloud instances will be needed based on historical usage patterns.

## Algorithm Overview

The predictor combines **two signals** with different logic for weekdays vs weekends:

| Day Type | Logic |
|----------|-------|
| **Weekday** | EMA (last 5 weekdays) + 3-week historical decay |
| **Weekend** | 3-week historical decay only |

## Flow Diagram

```
                    ┌─────────────────────────┐
                    │   Is target a weekend?  │
                    └───────────┬─────────────┘
                                │
              ┌─────────────────┴─────────────────┐
              ▼                                   ▼
         WEEKEND                              WEEKDAY
              │                                   │
              │                    ┌──────────────┴──────────────┐
              │                    ▼                             ▼
              │           ┌─────────────────┐    ┌──────────────────────┐
              │           │  calculateEMA   │    │calculateHistoricalWith│
              │           │ (last 5 weekdays)│    │        Decay         │
              │           └────────┬────────┘    └──────────┬───────────┘
              │                    │                        │
              │                    └──────────┬─────────────┘
              │                               ▼
              │                    ┌──────────────────┐
              │                    │  combineValues   │
              │                    │ 40% EMA + 60%    │
              │                    │    historical    │
              │                    └────────┬─────────┘
              │                             │
              ▼                             │
   ┌────────────────────────┐               │
   │calculateHistoricalWith │               │
   │         Decay          │               │
   └───────────┬────────────┘               │
               │                            │
               └───────────┬──────────────--┘
                           ▼
                  × Safety Buffer (1.1)
                           │
                           ▼
                     ceil() + min
                           │
                           ▼
               RecommendedInstances
```

## Components

### 1. EMA (Exponential Moving Average)
- Uses data from the **last 5 weekdays only** (excludes weekend data)
- Looks back up to 9 days to ensure 5 weekdays are captured
- Gives more weight to recent data points
- **Only used for weekday predictions**

### 2. 3-Week Historical Decay
- Looks at the same time window from 1, 2, and 3 weeks ago
- Uses **peak utilization** (not average) for safety margin
- Applies decay weights: Week 1 = 50%, Week 2 = 30%, Week 3 = 20%

## Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| `EMAPeriod` | 12 | Smoothing window size for EMA |
| `EMAWeight` | 0.4 | 40% EMA, 60% historical |
| `WeekDecayFactors` | [0.5, 0.3, 0.2] | Weights for week 1, 2, 3 |
| `SafetyBuffer` | 0.1 | Add 10% buffer |
| `MinInstances` | 1 | Minimum instances to recommend |

## Example

**Predicting for Saturday 2pm (Weekend):**

```
Step 1: Historical (3-week decay) = 10.0
Step 2: Safety buffer = 10.0 × 1.1 = 11.0
Step 3: Round up = ceil(11.0) = 11

Result: 11 instances recommended
```

**Predicting for Wednesday 2pm (Weekday):**

```
Step 1: EMA (last 5 weekdays) = 6.0
Step 2: Historical (3-week decay) = 10.0
Step 3: Combine = 0.4×6 + 0.6×10 = 8.4
Step 4: Safety buffer = 8.4 × 1.1 = 9.24
Step 5: Round up = ceil(9.24) = 10

Result: 10 instances recommended
```

