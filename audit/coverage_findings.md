# Test Coverage Findings

## Before: 25.2% Statement Coverage

### Functions at 0% Coverage (Before)
| File | Function | Coverage |
|------|----------|----------|
| domain.go | isDomainAllowed | 0% |
| domain.go | maybeAddDomain | 0% |
| fetch.go | fetchURL | 0% |
| handler.go | Handler | 0% |
| handler.go | processMessage | 0% |
| handler.go | extractDepth | 0% |
| handler.go | processHTMLContent | 0% |
| links.go | extractAndEnqueueLinks | 0% |
| links.go | enqueueLinks | 0% |
| main.go | NewCrawler | 0% |
| main.go | main | 0% |
| ratelimit.go | checkRateLimit | 0% |
| ratelimit.go | handleRateLimited | 0% |
| ratelimit.go | requeueWithDelay | 0% |
| robots.go | getRobots | 0% |
| robots.go | isAllowedByRobots | 0% |
| state.go | claimURL | 0% |
| state.go | markStatus | 0% |
| state.go | saveFetchResult | 0% |
| storage.go | uploadContent | 0% |
| storage.go | saveS3Keys | 0% |

### Functions Already Tested (Before)
| File | Function | Coverage |
|------|----------|----------|
| fetch.go | isPermanentHTTPError | 100% |
| fetch.go | isPrivateIP | 100% |
| fetch.go | validateHost | 85.7% |
| links.go | extractLinks | 90.9% |
| links.go | extractText | 94.7% |
| links.go | normalizeURL | 90.9% |
| storage.go | gzipCompress | 71.4% |
| storage.go | isHTML | 100% |
| url.go | hashURL | 100% |
| url.go | getDomain | 100% |
| url.go | getHost | 100% |

---

## After: 81.8% Statement Coverage

### Approach
1. Created AWS service interfaces (DynamoDBAPI, SQSAPI, S3API) to replace concrete types
2. Built mock implementations with configurable function callbacks
3. Used `mockRoundTripper` to test HTTP functions without real network calls
4. Table-driven tests following existing project conventions

### New Test Files
| File | Tests | Focus |
|------|-------|-------|
| mock_test.go | - | Mock types and test helpers |
| handler_test.go | 10 | SQS event handling, message processing, depth extraction |
| state_test.go | 7 | DynamoDB state transitions (claim, mark, save) |
| domain_test.go | 8 | Domain allowlist checking and auto-discovery |
| ratelimit_test.go | 8 | Rate limiting, requeue with delay, delay capping |
| robots_test.go | 11 | robots.txt fetching, caching, SSRF protection |
| enqueue_test.go | 6 | Batch SQS sends, dedup, domain blocking, partial failures |
| storage_test.go | +7 | S3 upload, saveS3Keys, pooled gzip, concurrent compression |
| fetch_test.go | +6 | fetchURL success/failure paths, SSRF, user-agent |
| security_test.go | 4 | SSRF-safe transport dialer control |
| benchmark_test.go | 8 | Performance benchmarks |

### Final Coverage by Function
| File | Function | Before | After |
|------|----------|--------|-------|
| domain.go | isDomainAllowed | 0% | **100%** |
| domain.go | maybeAddDomain | 0% | **100%** |
| fetch.go | fetchURL | 0% | **88.9%** |
| handler.go | Handler | 0% | **80%** |
| handler.go | processMessage | 0% | **92%** |
| handler.go | extractDepth | 0% | **100%** |
| handler.go | processHTMLContent | 0% | **91.7%** |
| links.go | enqueueLinks | 0% | **89.5%** |
| ratelimit.go | checkRateLimit | 0% | **100%** |
| ratelimit.go | handleRateLimited | 0% | **100%** |
| ratelimit.go | requeueWithDelay | 0% | **100%** |
| robots.go | getRobots | 0% | **75%** |
| robots.go | isAllowedByRobots | 0% | **85.7%** |
| state.go | claimURL | 0% | **100%** |
| state.go | markStatus | 0% | **100%** |
| state.go | saveFetchResult | 0% | **100%** |
| storage.go | uploadContent | 0% | **88.2%** |
| storage.go | saveS3Keys | 0% | **100%** |

### Remaining Uncovered (Expected)
| Function | Reason |
|----------|--------|
| main.go: NewCrawler | AWS config + env var setup, integration-only |
| main.go: main | Lambda bootstrap, integration-only |
| links.go: mustParseURL | Test helper only |
