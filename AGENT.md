# Agent Context

## Project
Distributed web crawler using AWS (SQS, DynamoDB) and Go.

## Principles
- One step at a time
- Deploy-time ≠ runtime
- SQS = delivery, DynamoDB = truth
- At-least-once delivery + exactly-once processing
- Cost safety first
- **Test each step before proceeding to the next**

## Current State
- **Phases 0-4**: Complete
- **Phase 5**: In progress (concurrency)
- **Current Step**: 5.1 — Prove race handling works

## Directory Structure
```
cdk/           → CDK infrastructure (Go)
producer/      → URL ingestion CLI
consumer/      → Message processor
```

## Environment Variables (runtime)
```
QUEUE_URL=<from CDK output>
TABLE_NAME=<from CDK output>
```

## Key Files
- `cdk/cdk-test.go` → Infrastructure definition
- `producer/main.go` → Enqueues URLs with dedup
- `consumer/main.go` → Processes messages exactly-once

## Correctness Guarantee
The consumer uses conditional DynamoDB update:
```
ConditionExpression: "#s = :queued"
```
Only ONE consumer wins the race. Losers ACK and exit.

## Working Rules
1. No placeholders in code
2. One change at a time
3. Explain before implementing
4. Keep files small and focused
5. **Do NOT proceed to next step until current step is tested and confirmed working**
6. **Wait for user confirmation after each step**
