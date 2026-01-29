# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Distributed web crawler on AWS using Go. SQS distributes URLs, Lambda fetches pages, DynamoDB tracks state and deduplication, S3 stores content. Core invariant: at-least-once delivery (SQS) + exactly-once processing (DynamoDB conditional update `#s = :queued` — only one Lambda wins the race).

## Build & Development Commands

```bash
# Root Makefile (preferred entry point)
make build          # Build Lambda bootstrap.zip
make test           # Test all modules
make deploy         # Build + cdk deploy
make clean          # Purge queue, clear table, clear bucket
make lint           # golangci-lint on all modules
make fmt            # gofmt all modules

# Lambda (custom build for deployment)
cd lambda && ./build.sh       # Produces bootstrap.zip (gzip compressed)
cd lambda && go build ./...   # Quick compile check
cd lambda && go test ./...    # Run tests
cd lambda && go test -run TestFunctionName ./...  # Single test

# CDK
cd cdk && go build ./...
cd cdk && go test ./...
STAGE=dev cdk deploy          # Deploy (STAGE defaults to "dev")
cdk destroy                   # Tear down stack

# Producer
cd producer && go run . "https://example.com"  # Enqueue a URL

# Cleanup
cd tools/cleanup && go run . --all    # Reset everything
cd tools/cleanup && go run . --queue  # Purge SQS only
cd tools/cleanup && go run . --table  # Clear DynamoDB only
cd tools/cleanup && go run . --bucket # Clear S3 only
```

## Architecture

**Modules** (Go workspace, all `go 1.25`):

| Module | Purpose |
|--------|---------|
| `cdk/` | AWS CDK infrastructure (stack name: `CrawlerStack-{STAGE}`) |
| `lambda/` | Serverless crawler — fetches URLs, extracts links, uploads to S3 |
| `producer/` | CLI to enqueue seed URLs with DynamoDB dedup |
| `consumer/` | Legacy polling worker (replaced by Lambda) |
| `tools/cleanup/` | CLI to purge queue, clear table, clear bucket |

**Lambda file organization** (`package main`, split by concern):
- `main.go` — Crawler struct, constants, initialization
- `handler.go` — SQS batch handler, message processing orchestration
- `fetch.go` — HTTP fetching, SSRF protection, error classification
- `robots.go` — robots.txt fetching and checking
- `ratelimit.go` — Per-domain rate limiting via DynamoDB
- `storage.go` — S3 upload (gzip compressed HTML + extracted text)
- `state.go` — DynamoDB state transitions (claimURL, markStatus, saveFetchResult)
- `links.go` — HTML link extraction, URL normalization, link enqueuing, domain discovery
- `domain.go` — Domain allowlist management
- `url.go` — URL hashing (SHA-256) and parsing helpers

**Data flow**: Producer → SQS → Lambda → {DynamoDB (state), S3 (content)} → SQS (discovered links, up to MAX_DEPTH=3)

**DynamoDB key patterns** (single table):
- `url_hash` — URL state tracking (queued → processing → fetched/failed)
- `domain#<host>` — Per-domain rate limiting (last_crawled_at)
- `allowed_domain#<host>` — Domain allowlist entries

## Key Conventions

- **Go style**: Early return on failure, no useless comments, short focused functions
- **Testing**: Table-driven tests with `[]struct` slices
- **Error handling**: Permanent HTTP errors (400, 401, 403, 404, 405, 410, 414, 451) are ACKed; retriable errors (5xx, network) return error for SQS retry
- **SSRF protection**: All fetched URLs validated against private IP ranges before request
- **Rate limiting**: Per-domain delay via DynamoDB; rate-limited URLs requeued with SQS delay

## Git Rules

- **Never commit binary files**: `lambda/bootstrap`, `lambda/bootstrap.zip`, `cdk/cdk-test`, `consumer/consumer`, `producer/producer`, `tools/cleanup/cleanup`
- If a binary appears in `git status`, run `git rm --cached <file>` before committing
- Pre-commit hooks run: trailing whitespace fix, AWS credential detection, go build, go test, golangci-lint

## CI

GitHub Actions (`.github/workflows/ci.yml`): builds all modules, tests all modules, lints `lambda/` and `cdk/` with golangci-lint v2.1.

## Environment

Runtime env vars sourced from `.env` (via godotenv in producer/consumer/cleanup):
```
QUEUE_URL=<SQS queue URL from CDK output>
TABLE_NAME=<DynamoDB table name from CDK output>
CONTENT_BUCKET=<S3 bucket name from CDK output>
```

Lambda receives these as CDK-configured environment variables.
