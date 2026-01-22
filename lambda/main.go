package main

import (
	"bytes"
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

	defaultMaxDepth    = 3    // Default max crawl depth
	defaultCrawlDelay  = 1000 // Default delay between requests to same domain (ms)
	robotsUserAgent    = "MyCrawler"
	domainKeyPrefix    = "domain#" // Prefix for domain rate limit keys in DynamoDB
)

type Crawler struct {
	ddb          *dynamodb.Client
	sqs          *sqs.Client
	httpClient   *http.Client
	tableName    string
	queueURL     string
	maxDepth     int
	crawlDelayMs int
	log          zerolog.Logger
	robotsCache  map[string]*robotstxt.RobotsData // Cache robots.txt per domain
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

	log.Info().Int("max_depth", maxDepth).Int("crawl_delay_ms", crawlDelayMs).Msg("Crawler initialized")

	return &Crawler{
		ddb: dynamodb.NewFromConfig(cfg),
		sqs: sqs.NewFromConfig(cfg),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		tableName:    tableName,
		queueURL:     queueURL,
		maxDepth:     maxDepth,
		crawlDelayMs: crawlDelayMs,
		log:          log,
		robotsCache:  make(map[string]*robotstxt.RobotsData),
	}, nil
}

func (c *Crawler) Handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	c.log.Info().Int("count", len(sqsEvent.Records)).Msg("Received batch")

	for _, record := range sqsEvent.Records {
		if err := c.processMessage(ctx, record); err != nil {
			c.log.Error().Err(err).Str("message_id", record.MessageId).Msg("Failed to process message")
		}
	}

	return nil
}

func (c *Crawler) processMessage(ctx context.Context, record events.SQSMessage) error {
	url := record.Body
	urlHash := hashURL(url)

	// Extract depth from message attributes (default 0 for seed URLs)
	depth := 0
	if depthAttr, ok := record.MessageAttributes["depth"]; ok && depthAttr.StringValue != nil {
		if parsed, err := strconv.Atoi(*depthAttr.StringValue); err == nil {
			depth = parsed
		}
	}

	c.log.Info().Str("url", url).Int("depth", depth).Msg("Processing")

	// Step 1: queued → processing (idempotent gate)
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &c.tableName,
		Key: map[string]dynamodbtypes.AttributeValue{
			"url_hash": &dynamodbtypes.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression: aws.String(
			"SET #s = :processing, processing_at = :now ADD attempts :one",
		),
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

	if err != nil {
		c.log.Warn().Str("url", url).Msg("LOST race — already claimed")
		return nil
	}

	c.log.Info().Str("url", url).Msg("WON race — checking robots.txt")

	// Step 2: Check robots.txt
	if !c.isAllowedByRobots(ctx, url) {
		c.log.Info().Str("url", url).Msg("Blocked by robots.txt")
		// Update status to robots_blocked
		_, err = c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: &c.tableName,
			Key: map[string]dynamodbtypes.AttributeValue{
				"url_hash": &dynamodbtypes.AttributeValueMemberS{Value: urlHash},
			},
			UpdateExpression: aws.String("SET #s = :status, finished_at = :now"),
			ExpressionAttributeNames: map[string]string{
				"#s": "status",
			},
			ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
				":status": &dynamodbtypes.AttributeValueMemberS{Value: stateRobotsBlocked},
				":now":    &dynamodbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
			},
		})
		return err
	}

	// Step 3: Check rate limit
	domain := getDomain(url)
	if !c.checkRateLimit(ctx, domain) {
		c.log.Info().Str("url", url).Str("domain", domain).Msg("Rate limited, re-queuing")
		// Reset status back to queued
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
		// Re-queue with delay (convert ms to seconds, minimum 1 second)
		delaySeconds := c.crawlDelayMs / 1000
		if delaySeconds < 1 {
			delaySeconds = 1
		}
		return c.requeueWithDelay(ctx, url, depth, delaySeconds)
	}

	// Step 4: Fetch the URL
	result := c.fetchURL(ctx, url)

	// Step 3: Update status based on fetch result
	ttl := time.Now().Add(7 * 24 * time.Hour).Unix()
	ttlStr := strconv.FormatInt(ttl, 10)

	var finalStatus string
	if result.Success {
		finalStatus = stateDone
	} else {
		finalStatus = stateFailed
	}

	_, err = c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &c.tableName,
		Key: map[string]dynamodbtypes.AttributeValue{
			"url_hash": &dynamodbtypes.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression: aws.String(
			"SET #s = :status, finished_at = :now, expires_at = :ttl, http_status = :http_status, content_length = :content_length, content_type = :content_type, fetch_duration_ms = :duration, fetch_error = :error, crawl_depth = :depth",
		),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":status":         &dynamodbtypes.AttributeValueMemberS{Value: finalStatus},
			":now":            &dynamodbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
			":ttl":            &dynamodbtypes.AttributeValueMemberN{Value: ttlStr},
			":http_status":    &dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(result.StatusCode)},
			":content_length": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(result.ContentLength, 10)},
			":content_type":   &dynamodbtypes.AttributeValueMemberS{Value: result.ContentType},
			":duration":       &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(result.DurationMs, 10)},
			":error":          &dynamodbtypes.AttributeValueMemberS{Value: result.Error},
			":depth":          &dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(depth)},
		},
	})

	if err != nil {
		c.log.Error().Err(err).Str("url", url).Msg("Failed to update status")
		return err
	}

	if result.Success {
		c.log.Info().
			Str("url", url).
			Int("status", result.StatusCode).
			Int64("bytes", result.ContentLength).
			Int64("ms", result.DurationMs).
			Msg("Fetched successfully")

		// Step 4: Extract and enqueue links from HTML pages (if not at max depth)
		if depth >= c.maxDepth {
			c.log.Info().
				Str("url", url).
				Int("depth", depth).
				Int("max_depth", c.maxDepth).
				Msg("Max depth reached, not extracting links")
		} else if isHTML(result.ContentType) && len(result.Body) > 0 {
			links := extractLinks(result.Body, url)
			c.log.Info().
				Str("url", url).
				Int("links_found", len(links)).
				Msg("Extracted links")

			// Enqueue discovered links (with deduplication) at depth+1
			enqueued := c.enqueueLinks(ctx, links, depth+1)
			if enqueued > 0 {
				c.log.Info().
					Str("url", url).
					Int("enqueued", enqueued).
					Int("skipped", len(links)-enqueued).
					Int("child_depth", depth+1).
					Msg("Enqueued new links")
			}
		}
	} else {
		c.log.Warn().
			Str("url", url).
			Int("status", result.StatusCode).
			Str("error", result.Error).
			Int64("ms", result.DurationMs).
			Msg("Fetch failed")
	}

	return nil
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

func (c *Crawler) fetchURL(ctx context.Context, url string) FetchResult {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
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
	defer resp.Body.Close()

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
func (c *Crawler) requeueWithDelay(ctx context.Context, urlStr string, depth int, delaySeconds int) error {
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
