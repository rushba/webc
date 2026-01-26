# Agent Context

## Project
Distributed web crawler using AWS (SQS, DynamoDB, Lambda, S3) and Go.

## Principles
- One step at a time
- Deploy-time ≠ runtime
- SQS = delivery, DynamoDB = truth
- At-least-once delivery + exactly-once processing
- Cost safety first
- **Test each step before proceeding to the next**

## Current State
- **Phases 1-11**: Complete (SQS, DynamoDB, Lambda, crawling, robots.txt, rate limiting, monitoring, content storage)
- **Phase 12**: Ready for next phase

## Directory Structure
```
cdk/           → CDK infrastructure (Go)
producer/      → URL ingestion CLI
consumer/      → Message processor (legacy, replaced by Lambda)
lambda/        → Serverless crawler
tools/cleanup/ → Cleanup CLI
```

## Environment Variables (runtime)
```
QUEUE_URL=<from CDK output>
TABLE_NAME=<from CDK output>
CONTENT_BUCKET=<from CDK output>  # Phase 11
```

## Key Files
- `cdk/cdk-test.go` → Infrastructure definition
- `lambda/main.go` → Serverless crawler
- `producer/main.go` → Enqueues URLs with dedup

## Correctness Guarantee
The Lambda uses conditional DynamoDB update:
```
ConditionExpression: "#s = :queued"
```
Only ONE Lambda wins the race. Losers ACK and exit.

## Go Style
- Early return on failure (no if/else with success first)
- No useless comments when code is self-documenting
- Extract methods with clear names instead of inline comments
- Keep functions short and focused

## Working Rules
1. No placeholders in code
2. One change at a time
3. Explain before implementing
4. Keep files small and focused
5. **Do NOT proceed to next step until current step is tested and confirmed working**
6. **Wait for user confirmation after each step**
7. **Commit after each step with user confirmation**
