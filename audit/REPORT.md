# Project Audit Report

## Executive Summary

Comprehensive three-track audit of the distributed web crawler: security vulnerabilities, performance bottlenecks, and test coverage gaps. All findings have been fixed, tested, and committed.

---

## Before/After Comparison

| Metric | Before | After | Change |
|--------|--------|-------|--------|
| **Test Coverage** | 25.2% | 81.8% | **+56.6 pp** |
| Functions at 0% | 21 | 3* | **-18 functions** |
| Functions at 100% | 11 | 20 | **+9 functions** |
| Security vulnerabilities | 2 | 0 | **All fixed** |
| Performance optimizations | 0 | 4 | **4 improvements** |
| Test files | 4 | 13 | **+9 files** |
| Test count | ~35 | ~100+ | **3x more tests** |

*Remaining 3 uncovered: main(), NewCrawler() (integration-only), mustParseURL() (test helper)

---

## Track 1: Security (2 findings, 2 fixed)

| # | Finding | Severity | Status |
|---|---------|----------|--------|
| S1 | TOCTOU DNS rebinding in SSRF protection | Critical | Fixed |
| S2 | Unbounded robots.txt cache (OOM risk) | Medium | Fixed |

**Key fix**: SSRF-safe transport with `net.Dialer.Control` function validates IPs at TCP connection time, eliminating DNS rebinding window. See [security_findings.md](security_findings.md).

---

## Track 2: Performance (4 improvements)

| # | Improvement | Speedup | Memory Reduction |
|---|-------------|---------|------------------|
| P1 | Batch SQS sends (10/batch) | Up to 10x fewer API calls | - |
| P2 | Parallel S3 uploads | ~50% upload latency | - |
| P3 | Pooled gzip writers | **43% faster** | **99.9% less** (814KB -> 753B) |
| P4 | Single-pass HTML parse | **41% faster** | **38% less** |

See [performance_findings.md](performance_findings.md) for full benchmark data.

---

## Track 3: Test Coverage (25.2% -> 81.8%)

| Category | Before | After |
|----------|--------|-------|
| handler.go | 0% | 80-100% |
| state.go | 0% | 100% |
| domain.go | 0% | 100% |
| ratelimit.go | 0% | 100% |
| robots.go | 0% | 75-100% |
| fetch.go | 47% | 85-100% |
| storage.go | 38% | 63-100% |
| links.go | 58% | 89-95% |

**Testing approach**: Created AWS service interfaces (DynamoDBAPI, SQSAPI, S3API) with mock implementations. Used `mockRoundTripper` for HTTP testing without real network calls.

See [coverage_findings.md](coverage_findings.md) for function-level detail.

---

## Commits

| Commit | Description |
|--------|-------------|
| 8bd3d18 | Add AWS service interfaces for testability |
| a8aa6bb | Fix TOCTOU DNS rebinding vulnerability in SSRF protection |
| 31fc118 | Add size limit to robots.txt cache to prevent OOM |
| b98503f | Improve performance: batch SQS, parallel S3, pooled gzip, single parse |
| b743140 | Add comprehensive unit tests for lambda module (25% -> 82% coverage) |

---

## Recommendations

1. **Integration tests**: Add integration tests for `NewCrawler()` and end-to-end message processing with DynamoDB Local
2. **CDK coverage**: The CDK module has 0% coverage (only a stack instantiation test exists)
3. **Consumer module**: The legacy consumer has no tests and uses `math/rand` for worker ID generation
4. **Redirect handling**: Consider treating 3xx responses as non-success or following redirects with SSRF validation on each hop
