# Phase 5 Plan — Concurrency & Scaling

## Goal
Run multiple consumers safely. Increase throughput without races.

---

## Steps

### Step 5.1 — Prove race handling works
**What**: Run 2 consumers in separate terminals against the same queue.
**Why**: Verify only one wins the `queued → processing` transition.
**How**: 
- Enqueue 1 URL
- Start consumer in terminal A
- Start consumer in terminal B
- Observe: one prints "WON", other prints "LOST" (already handled)

**Status**: [ ] Not started

---

### Step 5.2 — Add worker ID to consumer
**What**: Add `--worker-id` flag to identify which consumer processed what.
**Why**: Traceability when running multiple instances.
**How**: Small edit to `consumer/main.go`

**Status**: [ ] Not started

---

### Step 5.3 — Increase batch size
**What**: Change `MaxNumberOfMessages` from 1 to 10.
**Why**: Fewer network round trips, higher throughput.
**How**: Add `--batch-size` flag (1-10)

**Status**: [ ] Not started

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
→ **5.1 — Prove race handling works**

## Notes
- Don't add complexity until current step is proven
- Each step should be testable in isolation
