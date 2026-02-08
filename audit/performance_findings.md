# Performance Findings

## Finding 1: Sequential SQS Message Sends

**File**: `lambda/links.go`
**Status**: Fixed (commit b98503f)

### Description
`enqueueLinks()` sent individual `SendMessage` calls for each discovered link. A page with 50 links required 50 SQS API round-trips.

### Fix
Replaced with `SendMessageBatch` (up to 10 messages per API call). Links are now collected after DynamoDB dedup, then batch-sent. 50 links now require only 5 SQS API calls.

### Impact
- API round-trips reduced by up to 10x for link-heavy pages
- Reduced Lambda execution time proportional to number of links per page

---

## Finding 2: Sequential S3 Uploads

**File**: `lambda/storage.go`
**Status**: Fixed (commit b98503f)

### Description
`uploadContent()` uploaded raw HTML and extracted text to S3 sequentially. Each upload involves gzip compression + S3 PutObject, making total upload time ~2x a single upload.

### Fix
Both uploads now run concurrently using `errgroup.WithContext`. Errors from either upload cancel the other and propagate correctly.

### Impact
- Upload latency reduced by ~50% (parallel instead of sequential)

---

## Finding 3: GC-Heavy Gzip Compression

**File**: `lambda/storage.go`
**Status**: Fixed (commit b98503f)

### Description
`gzipCompress()` allocated a new `gzip.Writer` for each compression, creating significant GC pressure (814KB/op).

### Fix
Added `gzipCompressPooled()` using `sync.Pool` for `gzip.Writer` reuse. The original `gzipCompress()` is retained for backward compatibility in tests.

### Benchmark Results
```
gzipCompress:       166,254 ns/op  814,551 B/op  21 allocs/op
gzipCompressPooled:  95,901 ns/op      753 B/op   4 allocs/op
                     ^^^^^^            ^^^
                     43% faster     99.9% less memory
```

---

## Finding 4: Double HTML Parse

**Files**: `lambda/links.go`, `lambda/handler.go`
**Status**: Fixed (commit b98503f)

### Description
`processHTMLContent()` called `extractText(body)` and `extractLinks(body, url)` separately. Each function parsed the HTML DOM independently, doubling the parse work.

### Fix
Added `parseAndExtract()` which traverses the DOM once, extracting both links and text simultaneously. `processHTMLContent()` now uses this combined function.

### Benchmark Results
```
extractLinks+extractText (separate): 237,527 ns/op  220,954 B/op  2,456 allocs/op
parseAndExtract (combined):          141,069 ns/op  137,210 B/op  1,439 allocs/op
                                     ^^^^^^          ^^^^^^        ^^^^^
                                     41% faster      38% less mem  41% fewer allocs
```

---

## All Benchmark Results (Apple M1 Pro)

| Benchmark | Time | Memory | Allocs |
|-----------|------|--------|--------|
| ExtractLinks | 133.8 us | 120.7 KB | 1,429 |
| ExtractText | 101.8 us | 100.3 KB | 1,027 |
| ExtractLinks+ExtractText | 237.5 us | 220.9 KB | 2,456 |
| **parseAndExtract** | **141.1 us** | **137.2 KB** | **1,439** |
| NormalizeURL | 0.42 us | 0.36 KB | 5 |
| gzipCompress | 166.3 us | 814.6 KB | 21 |
| **gzipCompressPooled** | **95.9 us** | **0.75 KB** | **4** |
| HashURL | 0.16 us | 0.19 KB | 3 |
