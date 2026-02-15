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

## Phase 11 — COMPLETE ✓

Content storage working:
- [x] S3 bucket with 30-day lifecycle
- [x] Raw HTML + extracted text (gzip compressed)
- [x] S3 keys stored in DynamoDB
- [x] CloudWatch S3 metrics
- [x] Cleanup tool updated with --bucket flag

---

## Current Step
→ **Phase 12** — Multi-Domain Search Crawler

---

# Phase 12-15 Plan — Multi-Domain Search Crawler

## Goal
Extend the crawler to support multi-domain crawling and add a search layer using OpenSearch, enabling a small-scale search engine over crawled content.

## Target Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  Seed/Producer  │────▶│      SQS        │────▶│  Crawler Lambda │
└─────────────────┘     └─────────────────┘     └────────┬────────┘
                                                         │
                        ┌────────────────────────────────┼────────────────────────────────┐
                        │                                │                                │
                        ▼                                ▼                                ▼
               ┌─────────────────┐              ┌─────────────────┐              ┌─────────────────┐
               │    DynamoDB     │              │       S3        │              │   DynamoDB      │
               │   (URL state)   │              │  (raw + text)   │              │   (domains)     │
               └─────────────────┘              └────────┬────────┘              └─────────────────┘
                                                         │
                                                         ▼ S3 Event
                                               ┌─────────────────┐
                                               │  Index Lambda   │
                                               └────────┬────────┘
                                                         │
                                                         ▼
                                               ┌─────────────────┐
                                               │   OpenSearch    │
                                               │   (EC2 t3.small)│
                                               └────────┬────────┘
                                                         │
                                                         ▼
                                               ┌─────────────────┐
                                               │   Search API    │
                                               │  (API Gateway)  │
                                               └────────┬────────┘
                                                         │
                                                         ▼
                                               ┌─────────────────┐
                                               │     Web UI      │
                                               │   (S3 static)   │
                                               └─────────────────┘
```

---

## Phase 12: Multi-Domain Crawling

**Goal**: Remove same-domain restriction, add domain management via DynamoDB.

### Step 12.1 — Domain Table in DynamoDB

Add new items to existing DynamoDB table with prefix `allowed_domain#`:

```go
// Domain record structure
{
    "url_hash": "allowed_domain#example.com",  // PK with prefix
    "domain": "example.com",
    "status": "active",                         // active | paused | blocked
    "discovered_from": "seed",                  // seed | auto-discovered
    "created_at": "2026-01-28T10:00:00Z",
    "pages_crawled": 0
}
```

**Domain statuses**:
- `active` - crawl links from this domain
- `paused` - skip for now, can resume later
- `blocked` - never crawl (e.g., spam domains)

**Status**: [ ] Pending

---

### Step 12.2 — Update Lambda to Check Domain Table

Changes to `lambda/main.go`:

1. **Remove same-domain filter** in `normalizeURL()` (line 627-629)
2. **Add domain check before enqueuing**:

```go
func (c *Crawler) isDomainAllowed(ctx context.Context, domain string) bool {
    result, err := c.ddb.GetItem(ctx, &dynamodb.GetItemInput{
        TableName: &c.tableName,
        Key: map[string]types.AttributeValue{
            "url_hash": &types.AttributeValueMemberS{Value: "allowed_domain#" + domain},
        },
    })
    if err != nil || result.Item == nil {
        return false
    }
    status := result.Item["status"].(*types.AttributeValueMemberS).Value
    return status == "active"
}
```

**Status**: [ ] Pending

---

### Step 12.3 — Auto-Discovery of New Domains

When a link to a new domain is found:

```go
func (c *Crawler) maybeAddDomain(ctx context.Context, domain, discoveredFrom string) {
    _, _ = c.ddb.PutItem(ctx, &dynamodb.PutItemInput{
        TableName: &c.tableName,
        Item: map[string]types.AttributeValue{
            "url_hash":        &types.AttributeValueMemberS{Value: "allowed_domain#" + domain},
            "domain":          &types.AttributeValueMemberS{Value: domain},
            "status":          &types.AttributeValueMemberS{Value: "active"},
            "discovered_from": &types.AttributeValueMemberS{Value: discoveredFrom},
            "created_at":      &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
        },
        ConditionExpression: aws.String("attribute_not_exists(url_hash)"),
    })
}
```

**Status**: [ ] Pending

---

### Step 12.4 — Domain Management CLI

New `tools/domains/main.go`:

```bash
# List all domains
go run . --list

# Add a seed domain
go run . --add example.com

# Pause a domain
go run . --pause example.com

# Block a domain (spam)
go run . --block spamsite.com

# Show domain stats
go run . --stats
```

**Status**: [ ] Pending

---

## Phase 13: OpenSearch on EC2

**Goal**: Index crawled content for full-text search.
**Decision**: EC2-based OpenSearch (~$15/month) for hands-on learning.

### Step 13.1 — EC2 Instance Setup (via CDK)

**Instance spec**:
- AMI: Amazon Linux 2023
- Type: t3.small (2 vCPU, 2GB RAM) - ~$15/month
- Storage: 20GB gp3 EBS
- OpenSearch version: 2.x (single-node)

**UserData script** (installs OpenSearch on boot):
```bash
#!/bin/bash
yum install -y java-17-amazon-corretto
wget https://artifacts.opensearch.org/releases/bundle/opensearch/2.11.1/opensearch-2.11.1-linux-x64.tar.gz
tar -xzf opensearch-2.11.1-linux-x64.tar.gz
mv opensearch-2.11.1 /opt/opensearch
echo "discovery.type: single-node" >> /opt/opensearch/config/opensearch.yml
echo "network.host: 0.0.0.0" >> /opt/opensearch/config/opensearch.yml
echo "plugins.security.disabled: true" >> /opt/opensearch/config/opensearch.yml
/opt/opensearch/bin/opensearch -d
```

**Status**: [ ] Pending

---

### Step 13.2 — Create Search Index

Index mapping for crawled pages:
```json
{
  "mappings": {
    "properties": {
      "url": { "type": "keyword" },
      "domain": { "type": "keyword" },
      "title": { "type": "text", "analyzer": "standard" },
      "text": { "type": "text", "analyzer": "standard" },
      "crawled_at": { "type": "date" }
    }
  }
}
```

**Status**: [ ] Pending

---

### Step 13.3 — Index Lambda

New `indexer/` Lambda triggered by S3 PutObject events on text files:

**Flow**:
1. S3 event triggers Lambda (filter: `*/text.txt.gz`)
2. Read text content from S3
3. Fetch metadata (URL, title) from DynamoDB
4. POST document to OpenSearch `/_doc`

**Status**: [ ] Pending

---

### Step 13.4 — Lambda Network Access

Lambda needs to reach EC2:
- **Option A**: Lambda in same VPC as EC2 (add NAT Gateway - ~$30/month extra)
- **Option B**: EC2 with public IP, Lambda calls via public internet (simpler)

**Decision**: Start with Option B (public IP), migrate to VPC later.

**Status**: [ ] Pending

---

## Phase 14: Search API

**Goal**: REST API for searching crawled content.

### API Gateway + Lambda

```
GET /search?q=golang+tutorial&domain=example.com
```

Response:
```json
{
  "total": 42,
  "results": [
    {
      "url": "https://example.com/go-intro",
      "title": "Introduction to Go",
      "snippet": "...Go is a statically typed, compiled...",
      "score": 0.95
    }
  ]
}
```

**Status**: [ ] Pending

---

## Phase 15: Simple Web UI

**Goal**: Basic search interface.

**Approach**: Static S3 website with HTML/JS calling Search API.

**Features**:
- Search box
- Results list with title, URL, snippet
- Filter by domain
- Pagination

**Status**: [ ] Pending

---

## Cost Estimate (EC2 OpenSearch)

| Component                 | Monthly Cost |
| ------------------------- | ------------ |
| Lambda (crawl + index)    | ~$5          |
| DynamoDB                  | ~$5          |
| S3 (10GB)                 | ~$0.25       |
| SQS                       | ~$1          |
| EC2 t3.small (OpenSearch) | ~$15         |
| API Gateway               | ~$3          |
| **Total**                 | **~$30**     |

---

## Safety Considerations

With auto-discovery enabled, the crawler can grow rapidly. Consider adding:
- `MAX_DOMAINS` env var to cap total domains (e.g., 100)
- `MAX_PAGES_PER_DOMAIN` to prevent one domain from dominating
- Manual approval mode: auto-discovered domains start as `pending` instead of `active`

---

## Decisions Made

- **OpenSearch**: EC2-based (t3.small, ~$15/month) - for hands-on learning
- **Domain management**: DynamoDB table - dynamic add/remove without redeploy
- **External links**: Auto-discover - new domains added when found (organic growth)

---

# Project Audit — Technical Debt & Improvements

Audit performed on 2026-01-29. Covers Phases 1-12 (completed work).

---

## High Priority

### A1 — Split `lambda/main.go` into packages

**Problem**: `lambda/main.go` is 853 lines containing all logic: HTTP fetching, robots.txt, rate limiting, link extraction, S3 upload, domain management, and DynamoDB operations. Hard to test, reason about, and maintain.

**Recommendation**: Extract into internal packages:
```
lambda/
├── main.go              # Handler + wiring only
├── crawler/crawler.go   # Crawl orchestration (processMessage flow)
├── fetcher/fetcher.go   # HTTP fetching + redirect handling
├── robots/robots.go     # Robots.txt fetch, parse, cache
├── ratelimit/ratelimit.go # Domain-based rate limiting
├── storage/s3.go        # S3 upload + gzip compression
├── storage/dynamo.go    # DynamoDB state operations (claim, save, etc.)
├── links/extract.go     # HTML parsing + link extraction
└── domain/domain.go     # Domain allowlist + auto-discovery
```

**Status**: [ ] Pending

---

### A2 — Add unit tests for Lambda core functions

**Problem**: Zero automated tests. CDK tests are commented out. The Lambda has race condition handling (`claimURL`), HTML parsing (`extractLinks`, `extractText`), URL normalization, and domain logic — all untested.

**Recommendation**: Add tests for pure/isolated functions first:
1. `extractLinks()` — various HTML inputs, relative URLs, edge cases
2. `extractText()` — script/style removal, whitespace handling
3. `normalizeURL()` — fragments, query params, relative paths
4. `isDomainAllowed()` — mock DynamoDB, test active/paused/blocked
5. `claimURL()` — mock DynamoDB, test won/lost race scenarios

Use Go table-driven tests. Consider `testcontainers-go` or `dynamodblocal` for integration tests later.

**Status**: [ ] Pending

---

### A3 — Add SSRF protection to `fetchURL()`

**Problem**: `fetchURL()` follows HTTP redirects without validating the target. A malicious page could redirect the crawler to `http://169.254.169.254/` (AWS metadata endpoint) or internal VPC resources.

**Recommendation**: Add a `CheckRedirect` function to the HTTP client that validates each redirect target:
```go
client := &http.Client{
    Timeout: 10 * time.Second,
    CheckRedirect: func(req *http.Request, via []*http.Request) error {
        if isPrivateIP(req.URL.Hostname()) {
            return fmt.Errorf("redirect to private IP blocked: %s", req.URL.Host)
        }
        if len(via) >= 10 {
            return fmt.Errorf("too many redirects")
        }
        return nil
    },
}
```

Also validate the initial URL before fetching.

**Status**: [ ] Pending

---

### A4 — Set Lambda concurrency limit

**Problem**: No `reservedConcurrentExecutions` set on the Lambda function. A sudden spike in queued URLs could scale to hundreds of concurrent invocations, exceeding AWS account limits or generating unexpected costs.

**Recommendation**: Add to CDK stack:
```go
ReservedConcurrentExecutions: jsii.Number(10), // Start conservative
```

Adjust based on observed load and cost tolerance.

**Status**: [ ] Pending

---

## Medium Priority

### A5 — Standardize Go versions across modules

**Problem**: Inconsistent Go versions:
- `stack/` and `tools/cleanup/`: Go 1.23
- `lambda/`: Go 1.24
- `producer/` and `consumer/`: Go 1.25.5

**Recommendation**: Update all `go.mod` files to use Go 1.25.x. Run `go mod tidy` in each module.

**Status**: [ ] Pending

---

### A6 — Replace magic numbers with named constants

**Problem**: Hardcoded values scattered through `lambda/main.go`:
- `10 * 1024 * 1024` (10MB body limit)
- `10 * time.Second` (HTTP timeout)
- `7 * 24 * time.Hour` (DynamoDB TTL)
- `1000` (default crawl delay ms)
- `512 * 1024` (robots.txt max size)

**Recommendation**: Define constants at the top of the file:
```go
const (
    maxBodySize       = 10 * 1024 * 1024  // 10MB
    httpTimeout       = 10 * time.Second
    itemTTL           = 7 * 24 * time.Hour
    defaultCrawlDelay = 1000 // milliseconds
    maxRobotsTxtSize  = 512 * 1024
)
```

**Status**: [ ] Pending

---

### A7 — Add a Makefile

**Problem**: Common operations require remembering multiple commands across different directories. No single entry point for build/test/deploy/clean workflows.

**Recommendation**: Add a root `Makefile`:
```makefile
.PHONY: build test deploy clean lint

build:
	cd lambda && ./build.sh

test:
	cd lambda && go test ./...
	cd cdk && go test ./...

deploy: build
	cd cdk && cdk deploy

clean:
	cd tools/cleanup && go run . --all

lint:
	golangci-lint run ./...
```

**Status**: [ ] Pending

---

### A8 — Add environment parameterization to CDK

**Problem**: No way to deploy separate dev/staging/prod stacks. A single stack means testing changes risks production data.

**Recommendation**: Accept a stage parameter in the CDK stack:
```go
stage := os.Getenv("STAGE") // "dev" or "prod"
stackName := fmt.Sprintf("CrawlerStack-%s", stage)
```

Prefix all resource names with the stage. Allows `STAGE=dev cdk deploy` and `STAGE=prod cdk deploy` as independent stacks.

**Status**: [ ] Pending

---

### A9 — Add CI/CD pipeline (GitHub Actions)

**Problem**: No automated pipeline. Regressions can only be caught by pre-commit hooks locally.

**Recommendation**: Add `.github/workflows/ci.yml`:
```yaml
on: [push, pull_request]
jobs:
  build-and-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
      - run: cd lambda && go build ./...
      - run: cd lambda && go test ./...
      - run: cd cdk && go build ./...
      - uses: golangci/golangci-lint-action@v6
```

**Status**: [ ] Pending

---

### A10 — Distinguish retriable vs. permanent failures

**Problem**: Error handling in the Lambda is inconsistent. A 404 (permanent) is treated the same as a network timeout (retriable). Permanent failures retry up to 5 times before hitting the DLQ, wasting invocations.

**Recommendation**: After fetching, classify the HTTP status:
- **Permanent (ACK immediately)**: 400, 401, 403, 404, 410, 451
- **Retriable (let SQS retry)**: 429, 500, 502, 503, 504, network errors

Mark permanent failures as `status: failed` with a reason in DynamoDB.

**Status**: [ ] Pending

---

## Low Priority

### A11 — Add circuit breaker for failing domains

**Problem**: If a domain consistently returns 5xx errors, the crawler keeps retrying all URLs from it, wasting Lambda invocations and hammering a struggling server.

**Recommendation**: Track consecutive failures per domain in DynamoDB. After N failures (e.g., 5), auto-pause the domain for a cooldown period. Resume after the cooldown expires.

**Status**: [ ] Pending

---

### A12 — Improve S3 key structure

**Problem**: Current key format `<urlHash>/raw.html.gz` is flat. Cannot list or lifecycle-manage content by domain.

**Recommendation**: Change to `<domain>/<urlHash>/raw.html.gz`. This enables:
- S3 prefix-based listing per domain
- Per-domain lifecycle rules
- Easier cost attribution per domain

**Note**: This is a breaking change for existing stored content. Implement during a clean crawl.

**Status**: [ ] Pending

---

### A13 — Add content deduplication

**Problem**: Two different URLs returning identical content will both be stored in S3. Common with pagination, query parameter variations, and canonical URL issues.

**Recommendation**: Compute SHA256 of page content. Store the hash in DynamoDB. Before uploading to S3, check if content with the same hash already exists. If so, reference the existing S3 key instead of uploading a duplicate.

**Status**: [ ] Pending

---

### A14 — Evaluate `consumer/` module status

**Problem**: The `consumer/` CLI was replaced by the Lambda function for production use. It's unclear whether it's still useful for local development/testing or is dead code.

**Recommendation**: Either:
- **Keep it**: Add a note in AGENT.md that it's a local testing tool, not used in production
- **Remove it**: Delete the module to reduce maintenance surface

**Status**: [ ] Pending — decide on purpose

---

### A15 — Update User-Agent with contact info

**Problem**: `"MyCrawler/1.0"` doesn't include contact information. Well-behaved crawlers identify themselves with a URL or email so site operators can reach out about crawling issues.

**Recommendation**: Change to something like:
```
"MyCrawler/1.0 (+https://github.com/rushba/webc)"
```

**Status**: [ ] Pending

---

### A16 — Consider longer DynamoDB TTL

**Problem**: 7-day TTL on URL state items means URLs can be re-discovered and re-crawled after expiry, causing duplicate work and storage.

**Recommendation**: Evaluate whether 7 days is sufficient for your crawl cycle. Options:
- Increase TTL to 30 days (match S3 lifecycle)
- Add a lightweight "seen" record with longer TTL that just prevents re-enqueuing
- Accept re-crawling as a feature (content freshness)

**Status**: [ ] Pending — decide based on crawl frequency goals

---

## Audit Summary

| Priority | Count | Items |
|----------|-------|-------|
| High     | 4     | A1-A4 (split code, add tests, SSRF fix, concurrency limit) |
| Medium   | 6     | A5-A10 (Go versions, constants, Makefile, envs, CI, error handling) |
| Low      | 6     | A11-A16 (circuit breaker, S3 keys, dedup, consumer, user-agent, TTL) |

Recommended order: A3 (SSRF, quick security fix) → A4 (concurrency limit, quick) → A1 (split code) → A2 (tests) → A6 (constants) → A7 (Makefile) → rest as needed.

---

### A17 — Replace verbose DynamoDB attribute construction with `attributevalue` package

**Problem**: DynamoDB operations use raw `map[string]types.AttributeValue` with `&types.AttributeValueMemberS{Value: "..."}` everywhere. Verbose and hard to read.

**Recommendation**: Use `github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue` to marshal/unmarshal Go structs directly, similar to `encoding/json`. Touches every DynamoDB call — do as a standalone cleanup.

**Status**: [ ] Pending

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
**How**: Update `stack/stack.go`

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
