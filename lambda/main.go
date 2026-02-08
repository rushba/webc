package main

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/rs/zerolog"
	"github.com/temoto/robotstxt"
)

const (
	stateQueued        = "queued"
	stateProcessing    = "processing"
	stateDone          = "done"
	stateFailed        = "failed"
	stateRobotsBlocked = "robots_blocked"

	defaultMaxDepth        = 3    // Default max crawl depth
	defaultCrawlDelay      = 1000 // Default delay between requests to same domain (ms)
	robotsUserAgent        = "MyCrawler"
	domainKeyPrefix        = "domain#"         // Prefix for domain rate limit keys in DynamoDB
	allowedDomainKeyPrefix = "allowed_domain#" // Prefix for allowed domain keys in DynamoDB
	domainStatusActive     = "active"

	httpTimeout        = 10 * time.Second
	maxBodySize        = 10 * 1024 * 1024 // 10MB
	maxRobotsTxtSize   = 512 * 1024       // 512KB
	itemTTL            = 7 * 24 * time.Hour
	sqsMaxDelaySeconds = 900 // 15 minutes
)

type Crawler struct {
	ddb           DynamoDBAPI
	sqs           SQSAPI
	s3            S3API
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
		ddb: awsddb.NewFromConfig(cfg),
		sqs: awssqs.NewFromConfig(cfg),
		s3:  awss3.NewFromConfig(cfg),
		httpClient: &http.Client{
			Timeout: httpTimeout,
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

func main() {
	ctx := context.Background()

	crawler, err := NewCrawler(ctx)
	if err != nil {
		panic(err)
	}

	lambda.Start(crawler.Handler)
}
