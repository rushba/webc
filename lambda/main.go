package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/rs/zerolog"
	"github.com/temoto/robotstxt"
	"golang.org/x/net/html"
)

const (
	stateQueued        = "queued"
	stateProcessing    = "processing"
	stateDone          = "done"
	stateFailed        = "failed"
	stateRobotsBlocked = "robots_blocked"

	defaultMaxDepth   = 3    // Default max crawl depth
	defaultCrawlDelay = 1000 // Default delay between requests to same domain (ms)
	robotsUserAgent   = "MyCrawler"
	domainKeyPrefix   = "domain#" // Prefix for domain rate limit keys in DynamoDB
)

type Crawler struct {
	ddb           *dynamodb.Client
	sqs           *sqs.Client
	s3            *s3.Client
	httpClient    *http.Client
	tableName     string
	queueURL      string
	contentBucket string
	maxDepth      int
	crawlDelayMs  int
	log           zerolog.Logger
	robotsCache   map[string]*robotstxt.RobotsData // Cache robots.txt per domain
}

func NewCrawler(ctx context.Context) (*Crawler, error) {
	log := zerolog.New(os.Stdout).With().Timestamp().Logger()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	tableName := os.Getenv("TABLE_NAME")
	if tableName == "" {
		log.Fatal().Msg("TABLE_NAME environment variable not set")
	}

	queueURL := os.Getenv("QUEUE_URL")
	if queueURL == "" {
		log.Fatal().Msg("QUEUE_URL environment variable not set")
	}

	contentBucket := os.Getenv("CONTENT_BUCKET")
	if contentBucket == "" {
		log.Fatal().Msg("CONTENT_BUCKET environment variable not set")
	}

	maxDepth := defaultMaxDepth
	if maxDepthStr := os.Getenv("MAX_DEPTH"); maxDepthStr != "" {
		if parsed, err := strconv.Atoi(maxDepthStr); err == nil && parsed >= 0 {
			maxDepth = parsed
		}
	}

	crawlDelayMs := defaultCrawlDelay
	if delayStr := os.Getenv("CRAWL_DELAY_MS"); delayStr != "" {
		if parsed, err := strconv.Atoi(delayStr); err == nil && parsed >= 0 {
			crawlDelayMs = parsed
		}
	}

	log.Info().Int("max_depth", maxDepth).Int("crawl_delay_ms", crawlDelayMs).Str("content_bucket", contentBucket).Msg("Crawler initialized")

	return &Crawler{
		ddb: dynamodb.NewFromConfig(cfg),
		sqs: sqs.NewFromConfig(cfg),
		s3:  s3.NewFromConfig(cfg),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		tableName:     tableName,
		queueURL:      queueURL,
		contentBucket: contentBucket,
		maxDepth:      maxDepth,
		crawlDelayMs:  crawlDelayMs,
		log:           log,
		robotsCache:   make(map[string]*robotstxt.RobotsData),
	}, nil
}

func (c *Crawler) Handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	c.log.Info().Int("count", len(sqsEvent.Records)).Msg("Received batch")

	for i := range sqsEvent.Records {
		if err := c.processMessage(ctx, &sqsEvent.Records[i]); err != nil {
			c.log.Error().Err(err).Str("message_id", sqsEvent.Records[i].MessageId).Msg("Failed to process message")
		}
	}

	return nil
}

func (c *Crawler) processMessage(ctx context.Context, record *events.SQSMessage) error {
	targetURL := record.Body
	urlHash := hashURL(targetURL)
	depth := c.extractDepth(record)

	c.log.Info().Str("url", targetURL).Int("depth", depth).Msg("Processing")

	if !c.claimURL(ctx, urlHash) {
		c.log.Warn().Str("url", targetURL).Msg("LOST race — already claimed")
		return nil
	}
	c.log.Info().Str("url", targetURL).Msg("WON race — checking robots.txt")

	if !c.isAllowedByRobots(ctx, targetURL) {
		c.log.Info().Str("url", targetURL).Msg("Blocked by robots.txt")
		return c.markStatus(ctx, urlHash, stateRobotsBlocked)
	}

	if !c.checkRateLimit(ctx, getDomain(targetURL)) {
		return c.handleRateLimited(ctx, targetURL, urlHash, depth)
	}

	result := c.fetchURL(ctx, targetURL)
	if err := c.saveFetchResult(ctx, urlHash, &result, depth); err != nil {
		return err
	}

	if !result.Success {
		c.log.Warn().Str("url", targetURL).Int("status", result.StatusCode).Str("error", result.Error).Int64("ms", result.DurationMs).Msg("Fetch failed")
		return nil
	}

	c.log.Info().Str("url", targetURL).Int("status", result.StatusCode).Int64("bytes", result.ContentLength).Int64("ms", result.DurationMs).Msg("Fetched successfully")
	c.processHTMLContent(ctx, targetURL, urlHash, &result, depth)
	return nil
}

// extractDepth gets crawl depth from SQS message attributes
func (c *Crawler) extractDepth(record *events.SQSMessage) int {
	if depthAttr, ok := record.MessageAttributes["depth"]; ok && depthAttr.StringValue != nil {
		if parsed, err := strconv.Atoi(*depthAttr.StringValue); err == nil {
			return parsed
		}
	}
	return 0
}

// claimURL attempts to transition URL from queued → processing (returns true if won)
func (c *Crawler) claimURL(ctx context.Context, urlHash string) bool {
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &c.tableName,
		Key: map[string]dynamodbtypes.AttributeValue{
			"url_hash": &dynamodbtypes.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression:    aws.String("SET #s = :processing, processing_at = :now ADD attempts :one"),
		ConditionExpression: aws.String("#s = :queued"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":queued":     &dynamodbtypes.AttributeValueMemberS{Value: stateQueued},
			":processing": &dynamodbtypes.AttributeValueMemberS{Value: stateProcessing},
			":now":        &dynamodbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
			":one":        &dynamodbtypes.AttributeValueMemberN{Value: "1"},
		},
	})
	return err == nil
}

// markStatus sets a terminal status (robots_blocked, etc.)
func (c *Crawler) markStatus(ctx context.Context, urlHash, status string) error {
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &c.tableName,
		Key: map[string]dynamodbtypes.AttributeValue{
			"url_hash": &dynamodbtypes.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression: aws.String("SET #s = :status, finished_at = :now"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":status": &dynamodbtypes.AttributeValueMemberS{Value: status},
			":now":    &dynamodbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
		},
	})
	return err
}

// handleRateLimited resets URL to queued and re-queues with delay
func (c *Crawler) handleRateLimited(ctx context.Context, targetURL, urlHash string, depth int) error {
	c.log.Info().Str("url", targetURL).Str("domain", getDomain(targetURL)).Msg("Rate limited, re-queuing")

	// Reset to queued
	_, _ = c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &c.tableName,
		Key: map[string]dynamodbtypes.AttributeValue{
			"url_hash": &dynamodbtypes.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression: aws.String("SET #s = :queued"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":queued": &dynamodbtypes.AttributeValueMemberS{Value: stateQueued},
		},
	})

	delaySeconds := c.crawlDelayMs / 1000
	if delaySeconds < 1 {
		delaySeconds = 1
	}
	return c.requeueWithDelay(ctx, targetURL, depth, delaySeconds)
}

// saveFetchResult persists fetch metadata to DynamoDB
func (c *Crawler) saveFetchResult(ctx context.Context, urlHash string, result *FetchResult, depth int) error {
	status := stateDone
	if !result.Success {
		status = stateFailed
	}

	ttl := time.Now().Add(7 * 24 * time.Hour).Unix()
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &c.tableName,
		Key: map[string]dynamodbtypes.AttributeValue{
			"url_hash": &dynamodbtypes.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression: aws.String(
			"SET #s = :status, finished_at = :now, expires_at = :ttl, http_status = :http_status, " +
				"content_length = :content_length, content_type = :content_type, fetch_duration_ms = :duration, " +
				"fetch_error = :error, crawl_depth = :depth",
		),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":status":         &dynamodbtypes.AttributeValueMemberS{Value: status},
			":now":            &dynamodbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
			":ttl":            &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(ttl, 10)},
			":http_status":    &dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(result.StatusCode)},
			":content_length": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(result.ContentLength, 10)},
			":content_type":   &dynamodbtypes.AttributeValueMemberS{Value: result.ContentType},
			":duration":       &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(result.DurationMs, 10)},
			":error":          &dynamodbtypes.AttributeValueMemberS{Value: result.Error},
			":depth":          &dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(depth)},
		},
	})
	if err != nil {
		c.log.Error().Err(err).Str("url_hash", urlHash).Msg("Failed to update status")
	}
	return err
}

// processHTMLContent uploads content to S3 and extracts links
func (c *Crawler) processHTMLContent(ctx context.Context, targetURL, urlHash string, result *FetchResult, depth int) {
	if !isHTML(result.ContentType) || len(result.Body) == 0 {
		return
	}

	// Upload to S3
	text := extractText(result.Body)
	uploadResult, err := c.uploadContent(ctx, urlHash, result.Body, text)
	if err != nil {
		c.log.Error().Err(err).Str("url", targetURL).Msg("Failed to upload content to S3")
	} else {
		c.saveS3Keys(ctx, targetURL, urlHash, uploadResult, len(text))
	}

	// Extract and enqueue links
	c.extractAndEnqueueLinks(ctx, targetURL, result.Body, depth)
}

// saveS3Keys updates DynamoDB with S3 content locations
func (c *Crawler) saveS3Keys(ctx context.Context, targetURL, urlHash string, upload *UploadResult, textLen int) {
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &c.tableName,
		Key: map[string]dynamodbtypes.AttributeValue{
			"url_hash": &dynamodbtypes.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression: aws.String("SET s3_bucket = :bucket, s3_raw_key = :raw_key, s3_text_key = :text_key"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":bucket":   &dynamodbtypes.AttributeValueMemberS{Value: c.contentBucket},
			":raw_key":  &dynamodbtypes.AttributeValueMemberS{Value: upload.RawKey},
			":text_key": &dynamodbtypes.AttributeValueMemberS{Value: upload.TextKey},
		},
	})
	if err != nil {
		c.log.Error().Err(err).Str("url", targetURL).Msg("Failed to update DynamoDB with S3 keys")
		return
	}
	c.log.Info().Str("url", targetURL).Str("raw_key", upload.RawKey).Str("text_key", upload.TextKey).Int("text_len", textLen).Msg("Uploaded content to S3")
}

// extractAndEnqueueLinks discovers and queues new URLs from HTML
func (c *Crawler) extractAndEnqueueLinks(ctx context.Context, targetURL string, body []byte, depth int) {
	if depth >= c.maxDepth {
		c.log.Info().Str("url", targetURL).Int("depth", depth).Int("max_depth", c.maxDepth).Msg("Max depth reached, not extracting links")
		return
	}

	links := extractLinks(body, targetURL)
	c.log.Info().Str("url", targetURL).Int("links_found", len(links)).Msg("Extracted links")

	enqueued := c.enqueueLinks(ctx, links, depth+1)
	if enqueued > 0 {
		c.log.Info().Str("url", targetURL).Int("enqueued", enqueued).Int("skipped", len(links)-enqueued).Int("child_depth", depth+1).Msg("Enqueued new links")
	}
}

func hashURL(u string) string {
	h := sha256.Sum256([]byte(u))
	return hex.EncodeToString(h[:])
}

type FetchResult struct {
	Success       bool
	StatusCode    int
	ContentLength int64
	ContentType   string
	DurationMs    int64
	Error         string
	Body          []byte // For HTML pages, contains the body for link extraction
}

func (c *Crawler) fetchURL(ctx context.Context, targetURL string) FetchResult {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, http.NoBody)
	if err != nil {
		return FetchResult{
			Success:    false,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      "invalid request: " + err.Error(),
		}
	}

	req.Header.Set("User-Agent", "MyCrawler/1.0 (learning project)")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return FetchResult{
			Success:    false,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      err.Error(),
		}
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return FetchResult{
			Success:     false,
			StatusCode:  resp.StatusCode,
			ContentType: resp.Header.Get("Content-Type"),
			DurationMs:  time.Since(start).Milliseconds(),
			Error:       "read error: " + err.Error(),
		}
	}

	success := resp.StatusCode >= 200 && resp.StatusCode < 400
	contentType := resp.Header.Get("Content-Type")

	return FetchResult{
		Success:       success,
		StatusCode:    resp.StatusCode,
		ContentLength: int64(len(body)),
		ContentType:   contentType,
		DurationMs:    time.Since(start).Milliseconds(),
		Error:         "",
		Body:          body,
	}
}

// UploadResult contains S3 keys for uploaded content
type UploadResult struct {
	RawKey  string
	TextKey string
}

// uploadContent uploads raw HTML and extracted text to S3 with gzip compression
func (c *Crawler) uploadContent(ctx context.Context, urlHash string, rawHTML []byte, text string) (*UploadResult, error) {
	result := &UploadResult{
		RawKey:  urlHash + "/raw.html.gz",
		TextKey: urlHash + "/text.txt.gz",
	}

	// Upload raw HTML (gzip compressed)
	rawGz, err := gzipCompress(rawHTML)
	if err != nil {
		return nil, err
	}
	_, err = c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:          &c.contentBucket,
		Key:             &result.RawKey,
		Body:            bytes.NewReader(rawGz),
		ContentType:     aws.String("text/html"),
		ContentEncoding: aws.String("gzip"),
	})
	if err != nil {
		return nil, err
	}

	// Upload extracted text (gzip compressed)
	textGz, err := gzipCompress([]byte(text))
	if err != nil {
		return nil, err
	}
	_, err = c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:          &c.contentBucket,
		Key:             &result.TextKey,
		Body:            bytes.NewReader(textGz),
		ContentType:     aws.String("text/plain"),
		ContentEncoding: aws.String("gzip"),
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// gzipCompress compresses data using gzip
func gzipCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// enqueueLinks adds new URLs to DynamoDB and SQS queue (with deduplication)
func (c *Crawler) enqueueLinks(ctx context.Context, links []string, depth int) int {
	enqueued := 0
	depthStr := strconv.Itoa(depth)

	for _, link := range links {
		urlHash := hashURL(link)

		// Try to add to DynamoDB (will fail if already exists)
		_, err := c.ddb.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: &c.tableName,
			Item: map[string]dynamodbtypes.AttributeValue{
				"url_hash": &dynamodbtypes.AttributeValueMemberS{Value: urlHash},
				"url":      &dynamodbtypes.AttributeValueMemberS{Value: link},
				"status":   &dynamodbtypes.AttributeValueMemberS{Value: stateQueued},
			},
			ConditionExpression: aws.String("attribute_not_exists(url_hash)"),
		})
		if err != nil {
			// URL already exists - skip
			continue
		}

		// New URL - enqueue to SQS with depth attribute
		_, err = c.sqs.SendMessage(ctx, &sqs.SendMessageInput{
			QueueUrl:    &c.queueURL,
			MessageBody: &link,
			MessageAttributes: map[string]sqstypes.MessageAttributeValue{
				"depth": {
					DataType:    aws.String("Number"),
					StringValue: &depthStr,
				},
			},
		})
		if err != nil {
			c.log.Error().Err(err).Str("url", link).Msg("Failed to enqueue link")
			// Note: URL is in DynamoDB as "queued" but not in SQS - orphaned state
			// Could add cleanup logic here, but keeping simple for now
			continue
		}

		enqueued++
	}

	return enqueued
}

// isHTML checks if content type indicates HTML
func isHTML(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml")
}

// extractLinks parses HTML and extracts all <a href> links, normalizing them to absolute URLs
func extractLinks(body []byte, baseURLStr string) []string {
	baseURL, err := url.Parse(baseURLStr)
	if err != nil {
		return nil
	}

	var links []string
	seen := make(map[string]bool)

	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil
	}

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					link := normalizeURL(attr.Val, baseURL)
					if link != "" && !seen[link] {
						seen[link] = true
						links = append(links, link)
					}
					break
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			traverse(child)
		}
	}
	traverse(doc)

	return links
}

// extractText parses HTML and extracts visible text content
func extractText(body []byte) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}

	var sb strings.Builder
	var extractNode func(*html.Node)
	extractNode = func(n *html.Node) {
		// Skip non-visible elements
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "head", "meta", "link":
				return
			}
		}

		// Extract text nodes
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				if sb.Len() > 0 {
					sb.WriteString(" ")
				}
				sb.WriteString(text)
			}
		}

		// Recurse into children
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			extractNode(child)
		}
	}
	extractNode(doc)

	return sb.String()
}

// normalizeURL converts a potentially relative URL to an absolute URL
// Returns empty string for URLs we don't want to crawl
func normalizeURL(href string, baseURL *url.URL) string {
	href = strings.TrimSpace(href)

	// Skip empty, fragments, javascript, mailto, tel, etc.
	if href == "" ||
		strings.HasPrefix(href, "#") ||
		strings.HasPrefix(href, "javascript:") ||
		strings.HasPrefix(href, "mailto:") ||
		strings.HasPrefix(href, "tel:") ||
		strings.HasPrefix(href, "data:") {
		return ""
	}

	// Parse the href
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}

	// Resolve relative URLs against base
	resolved := baseURL.ResolveReference(parsed)

	// Only keep http/https
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}

	// Remove fragment
	resolved.Fragment = ""

	// Same-domain filter: only crawl links on the same host
	if resolved.Host != baseURL.Host {
		return ""
	}

	return resolved.String()
}

// getRobots fetches and caches robots.txt for a domain
func (c *Crawler) getRobots(ctx context.Context, urlStr string) *robotstxt.RobotsData {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return nil
	}

	domain := parsed.Scheme + "://" + parsed.Host

	// Check cache first
	if robots, ok := c.robotsCache[domain]; ok {
		return robots
	}

	// Fetch robots.txt
	robotsURL := domain + "/robots.txt"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, http.NoBody)
	if err != nil {
		c.robotsCache[domain] = nil // Cache the failure
		return nil
	}
	req.Header.Set("User-Agent", robotsUserAgent+"/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.log.Debug().Str("domain", domain).Err(err).Msg("Failed to fetch robots.txt")
		c.robotsCache[domain] = nil
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	// If not found or error, allow all
	if resp.StatusCode != http.StatusOK {
		c.log.Debug().Str("domain", domain).Int("status", resp.StatusCode).Msg("robots.txt not found, allowing all")
		c.robotsCache[domain] = nil
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024)) // 512KB max
	if err != nil {
		c.robotsCache[domain] = nil
		return nil
	}

	robots, err := robotstxt.FromBytes(body)
	if err != nil {
		c.log.Warn().Str("domain", domain).Err(err).Msg("Failed to parse robots.txt")
		c.robotsCache[domain] = nil
		return nil
	}

	c.log.Info().Str("domain", domain).Msg("Loaded robots.txt")
	c.robotsCache[domain] = robots
	return robots
}

// isAllowedByRobots checks if a URL is allowed by robots.txt
func (c *Crawler) isAllowedByRobots(ctx context.Context, urlStr string) bool {
	robots := c.getRobots(ctx, urlStr)
	if robots == nil {
		// No robots.txt or failed to fetch - allow by default
		return true
	}

	parsed, err := url.Parse(urlStr)
	if err != nil {
		return true
	}

	// Check if the path is allowed for our user agent
	return robots.TestAgent(parsed.Path, robotsUserAgent)
}

// getDomain extracts the domain (scheme + host) from a URL
func getDomain(urlStr string) string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// checkRateLimit checks if we can crawl the domain (enough time since last crawl)
// Returns true if allowed, false if rate limited
func (c *Crawler) checkRateLimit(ctx context.Context, domain string) bool {
	if c.crawlDelayMs <= 0 {
		return true // No rate limiting
	}

	domainKey := domainKeyPrefix + domain
	now := time.Now().UnixMilli()
	nowStr := strconv.FormatInt(now, 10)
	minTime := now - int64(c.crawlDelayMs)
	minTimeStr := strconv.FormatInt(minTime, 10)

	// Try to update last_crawled_at with condition: either doesn't exist or is old enough
	_, err := c.ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &c.tableName,
		Item: map[string]dynamodbtypes.AttributeValue{
			"url_hash":        &dynamodbtypes.AttributeValueMemberS{Value: domainKey},
			"last_crawled_at": &dynamodbtypes.AttributeValueMemberN{Value: nowStr},
			"domain":          &dynamodbtypes.AttributeValueMemberS{Value: domain},
		},
		// Only succeed if: key doesn't exist OR last_crawled_at < minTime
		ConditionExpression: aws.String("attribute_not_exists(url_hash) OR last_crawled_at < :min_time"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":min_time": &dynamodbtypes.AttributeValueMemberN{Value: minTimeStr},
		},
	})
	if err != nil {
		// Condition failed = rate limited
		c.log.Debug().Str("domain", domain).Int("delay_ms", c.crawlDelayMs).Msg("Rate limited")
		return false
	}

	return true
}

// requeueWithDelay sends the URL back to the queue with a delay
func (c *Crawler) requeueWithDelay(ctx context.Context, urlStr string, depth, delaySeconds int) error {
	depthStr := strconv.Itoa(depth)

	// Cap delay at SQS max (900 seconds = 15 minutes)
	if delaySeconds > 900 {
		delaySeconds = 900
	}

	_, err := c.sqs.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:     &c.queueURL,
		MessageBody:  &urlStr,
		DelaySeconds: int32(delaySeconds),
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			"depth": {
				DataType:    aws.String("Number"),
				StringValue: &depthStr,
			},
		},
	})

	return err
}

func main() {
	ctx := context.Background()

	crawler, err := NewCrawler(ctx)
	if err != nil {
		panic(err)
	}

	lambda.Start(crawler.Handler)
}
