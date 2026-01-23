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

## Phase 6 — COMPLETE ✓

All Lambda crawler steps finished:
- [x] 6.1 — Cleanup CLI tool
- [x] 6.2 — Lambda handler created
- [x] 6.3 — Lambda added to CDK
- [x] 6.4 — Deployed and tested (end-to-end working)

---

## Phase 7 — COMPLETE ✓

Core crawling functionality working:
- [x] 7.1 — Link extraction (golang.org/x/net/html)
- [x] 7.2 — SQS send permission granted
- [x] 7.3 — Dedup + enqueue logic
- [x] 7.4 — Depth limiting (MAX_DEPTH=3, recursive loop allowed)
- [x] 7.5 — End-to-end test (800 pages with depth limit)

---

## Phase 8 — COMPLETE ✓

Robots.txt support working:
- [x] 8.1 — Add robotstxt library
- [x] 8.2 — Fetch and cache robots.txt
- [x] 8.3 — Check URL before crawling
- [x] 8.4 — Tested with httpbin.org

---

## Phase 9 — COMPLETE ✓

Rate limiting working:
- [x] 9.1 — Track last crawl time per domain
- [x] 9.2 — Check delay before crawling (atomic DynamoDB update)
- [x] 9.3 — Re-queue with SQS delay
- [x] 9.4 — Tested with httpbin.org

---

## Phase 10 — COMPLETE ✓

Monitoring deployed:
- [x] CloudWatch Dashboard (6 widgets)
- [x] 3 Alarms → SNS Topic
- [x] Resources verified

---

## Current Step
→ **Phase 11** — Ready for next phase

---

# Phase 10 Plan — Monitoring

## Goal
Add CloudWatch dashboard and alerts for visibility into crawler health.

---

## Steps

### Step 10.1 — CloudWatch Dashboard
**What**: Create dashboard with key metrics.
**Widgets**:
- Lambda Invocations & Errors
- Lambda Duration
- Concurrent Executions
- Queue Depth
- Message Age
- Dead Letter Queue

**Status**: [x] Complete

---

### Step 10.2 — Error Alerts
**What**: SNS alerts for failures.
**Alarms**:
- Lambda errors > 5 in 2 periods → SNS
- DLQ messages > 0 → SNS
- Lambda p95 duration > 25s → SNS

**Status**: [x] Complete

---

### Step 10.3 — Test Monitoring
**What**: Verify dashboard and alerts work.

**Results**:
- Dashboard deployed: CrawlerDashboard
- 3 alarms created, all connected to SNS topic
- Alarms in INSUFFICIENT_DATA (normal for new alarms)

**Status**: [x] Complete

---

# Phase 9 Plan — Rate Limiting

## Goal
Be polite to servers by adding delays between requests to the same domain.

## Challenge
Lambda is stateless and runs in parallel. Need to coordinate across invocations.

## Approach
1. Store `last_crawled_at` per domain in DynamoDB
2. Before crawling, check if enough time has passed
3. If too soon, re-queue the message with a delay
4. Configurable delay via `CRAWL_DELAY_MS` env var (default 1000ms)

---

## Steps

### Step 9.1 — Track last crawl time per domain
**What**: Store when we last crawled each domain.
**Why**: Need to know when to allow next request.
**How**: Store `domain#<url>` keys in existing DynamoDB table.

**Status**: [x] Complete

---

### Step 9.2 — Check delay before crawling
**What**: Before fetching, verify enough time has passed.
**Why**: Respect the delay between requests.
**How**:
- Atomic conditional PutItem to check and update last_crawled_at
- If condition fails (too soon), re-queue with delay

**Status**: [x] Complete

---

### Step 9.3 — Re-queue with delay
**What**: Send message back to queue with visibility delay.
**Why**: Wait before retrying the URL.
**How**: Use SQS DelaySeconds (converts CRAWL_DELAY_MS to seconds)

**Status**: [x] Complete

---

### Step 9.4 — Test rate limiting
**What**: Verify delays are enforced.
**Why**: Ensure it works correctly.

**Results**:
- 3 URLs to same domain sent rapidly
- First: fetched immediately
- Second & third: "Rate limited, re-queuing"
- After delay: all completed successfully

**Status**: [x] Complete

---

## Notes
- Default delay: 1000ms (1 second between requests to same domain)
- SQS max delay: 900 seconds
- Could use Crawl-delay from robots.txt in future

---

# Phase 8 Plan — Robots.txt Support

## Goal
Respect robots.txt rules to be a polite crawler.

## How it works
```
1. Before crawling a URL, fetch robots.txt for that domain
2. Parse the rules for our User-Agent
3. Check if the URL path is allowed
4. Skip disallowed URLs (mark as "skipped" in DynamoDB)
5. Cache robots.txt per domain to avoid repeated fetches
```

---

## Steps

### Step 8.1 — Add robots.txt parsing
**What**: Add a library to parse robots.txt files.
**Why**: Need to understand Allow/Disallow rules.
**How**: Use `github.com/temoto/robotstxt` package.

**Status**: [x] Complete

---

### Step 8.2 — Fetch and cache robots.txt
**What**: Before crawling, fetch robots.txt for the domain.
**Why**: Need the rules before checking URLs.
**How**: 
- In-memory cache per Lambda invocation
- Handle 404 gracefully (allow all)
- 512KB max size limit

**Status**: [x] Complete

---

### Step 8.3 — Check URL before crawling
**What**: Skip URLs that are disallowed by robots.txt.
**Why**: Respect site owner's wishes.
**How**:
- Before fetching, check if URL is allowed
- If disallowed, mark status as "robots_blocked" in DynamoDB
- Don't extract links from blocked pages

**Status**: [x] Complete

---

### Step 8.4 — Test with real sites
**What**: Verify robots.txt is respected.
**Why**: Ensure it works correctly.
**How**: Test with sites that have known robots.txt rules.

**Results**:
- httpbin.org/deny → `robots_blocked` ✓
- httpbin.org/get → `done` ✓

**Status**: [x] Complete

---

## Notes
- User-Agent: "MyCrawler/1.0"
- Fallback: If robots.txt fetch fails, allow crawling (be lenient)
- Crawl-delay: Could implement in future phase

---

# Phase 7 Plan — Link Extraction (Actual Crawling)

## Goal
Extract links from fetched pages and feed them back to the queue. Turn the fetcher into a real crawler.

## Architecture
```
Producer → SQS → Lambda → HTTP fetch → Parse HTML → Extract links
              ↑                                          ↓
              └──────────── New URLs (deduplicated) ─────┘
```

---

## Steps

### Step 7.1 — Add link extraction to Lambda
**What**: Parse HTML response, extract `<a href>` links.
**Why**: Discover new URLs to crawl.
**How**: Use `golang.org/x/net/html` to parse and extract links.

**Changes to `lambda/main.go`**:
- Add `extractLinks(body []byte, baseURL string) []string`
- Normalize relative URLs to absolute
- Filter: same-domain only, skip fragments/javascript/mailto

**Status**: [x] Complete — 73 links extracted from books.toscrape.com test

---

### Step 7.2 — Grant Lambda SQS send permission
**What**: Allow Lambda to send messages to the queue.
**Why**: Lambda needs to enqueue discovered links.
**How**: Update CDK to grant `queue.GrantSendMessages(crawlerLambda)`

**Status**: [x] Complete

---

### Step 7.3 — Add dedup + enqueue logic to Lambda
**What**: For each extracted link, check DynamoDB and enqueue if new.
**Why**: Avoid re-crawling known URLs.
**How**: Same pattern as producer - conditional PutItem, then SendMessage.

**New Lambda env vars**:
- `QUEUE_URL` (already have `TABLE_NAME`)

**Status**: [x] Complete — tested with books.toscrape.com, discovered 1000+ pages

---

### Step 7.4 — Add crawl depth limiting
**What**: Track crawl depth, stop at max depth.
**Why**: Prevent infinite crawling and runaway costs.
**How**: 
- Add `depth` field to DynamoDB item
- Pass depth in SQS message attributes
- Stop extracting links when depth >= MAX_DEPTH
- Disable AWS recursive loop detection (intentional pattern)

**Results**:
- MAX_DEPTH=3: Crawled 800 pages (vs 1,115 unlimited)
- Logs show "Max depth reached, not extracting links"

**Status**: [x] Complete

---

### Step 7.5 — Test end-to-end crawl
**What**: Seed with one URL, verify it discovers and crawls linked pages.
**Why**: Prove the crawler works.
**How**: Use a site with known link structure (e.g., `books.toscrape.com`)

**Results**:
- Seeded: `https://books.toscrape.com/`
- Discovered: **1,115 pages**
- Crawl completed successfully

**Status**: [x] Complete

---

## Notes
- Start with same-domain crawling only (no external links)
- Respect robots.txt? (future phase)
- Rate limiting per domain? (future phase)
- Max URLs per page to extract? (prevent spam pages)

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

**Status**: [x] Complete

---

### Step 6.4 — Deploy and test
**What**: Deploy Lambda, enqueue URLs, verify processing.
**Why**: End-to-end validation.
**How**: `cdk deploy`, then use producer + check DynamoDB.

**Status**: [x] Complete — verified working (event source mapping recreated after stuck state)

---

## Notes
- Lambda timeout: 30s (> 10s HTTP timeout + buffer)
- Lambda memory: 128MB
- SQS triggers Lambda automatically (no polling code needed)
- Consumer CLI remains for local testing if needed

## Notes
- Don't add complexity until current step is proven
- Each step should be testable in isolation
