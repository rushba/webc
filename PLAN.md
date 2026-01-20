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
**Decision**: Keep 30s — provides safe margin over 5s simulated processing.

**Status**: [x] Complete — no change needed

---

### Step 5.5 — Observe SQS metrics
**What**: Check CloudWatch metrics for the queue.
**Metrics**:
- `ApproximateNumberOfMessagesVisible` — queue depth
- `ApproximateNumberOfMessagesNotVisible` — in-flight
- `NumberOfMessagesReceived` — throughput
- `ApproximateAgeOfOldestMessage` — latency/backlog

**How**: 
```bash
QUEUE_NAME=$(basename $QUEUE_URL)
aws cloudwatch get-metric-statistics \
  --namespace AWS/SQS \
  --metric-name ApproximateNumberOfMessagesVisible \
  --dimensions Name=QueueName,Value=$QUEUE_NAME \
  --start-time $(date -u -v-1H +%Y-%m-%dT%H:%M:%SZ) \
  --end-time $(date -u +%Y-%m-%dT%H:%M:%SZ) \
  --period 300 \
  --statistics Average
```

**Status**: [x] Complete

---

## Phase 5 — COMPLETE ✓

All concurrency & scaling steps finished:
- [x] 5.1 — Race handling proven
- [x] 5.2 — Worker ID + zerolog logging
- [x] 5.3 — Batch size flag (1-10)
- [x] 5.4 — Visibility timeout (30s)
- [x] 5.5 — CloudWatch metrics observed

---

## Current Step
→ **6.2 — Create Lambda handler** (ready to test locally)

---

# Phase 6 Plan — Lambda Crawler + Cleanup Tool

## Goal
Run the crawler as AWS Lambda triggered by SQS. Add cleanup tool for testing.

## Architecture
```
Producer → SQS → Lambda (auto-triggered) → DynamoDB
                    ↓
                HTTP fetch
```

---

## Steps

### Step 6.1 — Create cleanup CLI tool
**What**: CLI to purge SQS queue and clear DynamoDB table.
**Why**: Fresh state for testing.
**How**: New `tools/cleanup/main.go`

**Usage**:
```bash
cd tools/cleanup
go run . --all        # Purge queue + clear table
go run . --queue      # Purge queue only
go run . --table      # Clear table only
```

**Status**: [x] Complete

---

### Step 6.2 — Create Lambda handler
**What**: Lambda function that receives SQS events and fetches URLs.
**Why**: Serverless, auto-scaling, pay-per-use.
**How**: New `lambda/main.go` with SQS event handler.

**Files**:
```
lambda/
├── main.go
└── go.mod
```

**Status**: [x] Complete

---

### Step 6.3 — Add Lambda to CDK
**What**: Define Lambda function in CDK with SQS trigger.
**Why**: Infrastructure as code.
**How**: Update `cdk/cdk-test.go`

**Changes**:
- Added `awslambda.NewFunction` with:
  - Runtime: `PROVIDED_AL2023` (custom Go binary)
  - Architecture: `ARM64` (cheaper)
  - Memory: 128MB
  - Timeout: 30s
  - Environment: `TABLE_NAME`
- Added SQS event source trigger (batch size 10)
- Granted DynamoDB read/write permissions
- Increased queue visibility timeout to 60s (must be >= Lambda timeout)

**Build Lambda first**:
```bash
cd lambda
chmod +x build.sh
./build.sh
```

**Status**: [ ] Ready to test (cdk diff)

---

### Step 6.4 — Deploy and test
**What**: Deploy Lambda, enqueue URLs, verify processing.
**Why**: End-to-end validation.
**How**: `cdk deploy`, then use producer + check DynamoDB.

**Status**: [ ] Not started

---

## Notes
- Lambda timeout: 30s (> 10s HTTP timeout + buffer)
- Lambda memory: 128MB
- SQS triggers Lambda automatically (no polling code needed)
- Consumer CLI remains for local testing if needed

## Notes
- Don't add complexity until current step is proven
- Each step should be testable in isolation
