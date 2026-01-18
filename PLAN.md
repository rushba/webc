# Phase 5 Plan — Concurrency & Scaling

## Goal
Run multiple consumers safely. Increase throughput without races.

---

## Steps

### Step 5.1 — Prove race handling works
**What**: Run 2 consumers in separate terminals against the same queue.
**Why**: Verify only one wins the `queued → processing` transition.
**How**: 
1. Reset state: delete any existing items in DynamoDB table
2. Enqueue 1 URL using producer
3. Start consumer in terminal A with `--continuous`
4. Start consumer in terminal B with `--continuous`
5. Observe: one prints "WON race", other prints "LOST race"

**Test Commands**:
```bash
# Terminal 1: Enqueue a test URL
cd producer && go run . "https://example.com/test-race-$(date +%s)"

# Terminal 2: Start consumer A
cd consumer && go run . --continuous

# Terminal 3: Start consumer B  
cd consumer && go run . --continuous
```

**Expected Output**:
- Consumer A: `WON race — claimed for processing: <url>`
- Consumer B: `LOST race — already claimed by another consumer: <url>`
  (or vice versa)

**Status**: [x] Complete

---

### Step 5.2 — Add worker ID to consumer
**What**: Add `--worker-id` flag to identify which consumer processed what.
**Why**: Traceability when running multiple instances.
**How**: Small edit to `consumer/main.go`

**Flags**:
```bash
--worker-id=worker-A    # Custom ID (default: random 6-char)
--log-format=console    # console (colored) or json
--log-level=info        # debug, info, warn, error
```

**Usage**:
```bash
# Colored console (development)
go run . --continuous --worker-id=worker-A

# JSON output (production / log files)
go run . --continuous --worker-id=worker-A --log-format=json

# Debug level (see "No messages" polling)
go run . --continuous --log-level=debug
```

**Status**: [x] Complete — zerolog added

---

### Step 5.3 — Increase batch size
**What**: Change `MaxNumberOfMessages` from 1 to 10.
**Why**: Fewer network round trips, higher throughput.
**How**: Add `--batch-size` flag (1-10)

**Usage**:
```bash
# Default (1 message per poll)
go run . --continuous

# Batch of 10 messages per poll
go run . --continuous --batch-size=10
```

**Status**: [x] Complete

---

### Step 5.4 — Tune visibility timeout
**What**: Understand and adjust `VisibilityTimeout`.
**Why**: If processing takes longer than timeout, message reappears → duplicate delivery.
**Current**: 30 seconds (set in CDK)
**Rule**: VisibilityTimeout > max processing time

**Status**: [ ] Not started

---

### Step 5.5 — Observe SQS metrics
**What**: Check CloudWatch metrics for the queue.
**Metrics**:
- `ApproximateNumberOfMessagesVisible` — queue depth
- `ApproximateNumberOfMessagesNotVisible` — in-flight
- `NumberOfMessagesReceived` — throughput
- `ApproximateAgeOfOldestMessage` — latency/backlog

**How**: AWS Console or CLI

**Status**: [ ] Not started

---

## Current Step
→ **5.4 — Tune visibility timeout** (next)

## Notes
- Don't add complexity until current step is proven
- Each step should be testable in isolation
