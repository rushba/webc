package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/rs/zerolog"
)

const (
	stateQueued     = "queued"
	stateProcessing = "processing"
	stateDone       = "done"
	stateFailed     = "failed"
)

type Crawler struct {
	ddb        *dynamodb.Client
	httpClient *http.Client
	tableName  string
	log        zerolog.Logger
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

	return &Crawler{
		ddb: dynamodb.NewFromConfig(cfg),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		tableName: tableName,
		log:       log,
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

	c.log.Info().Str("url", url).Msg("Processing")

	// Step 1: queued → processing (idempotent gate)
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &c.tableName,
		Key: map[string]types.AttributeValue{
			"url_hash": &types.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression: aws.String(
			"SET #s = :processing, processing_at = :now ADD attempts :one",
		),
		ConditionExpression: aws.String("#s = :queued"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":queued":     &types.AttributeValueMemberS{Value: stateQueued},
			":processing": &types.AttributeValueMemberS{Value: stateProcessing},
			":now":        &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
			":one":        &types.AttributeValueMemberN{Value: "1"},
		},
	})

	if err != nil {
		c.log.Warn().Str("url", url).Msg("LOST race — already claimed")
		return nil
	}

	c.log.Info().Str("url", url).Msg("WON race — fetching")

	// Step 2: Fetch the URL
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
		Key: map[string]types.AttributeValue{
			"url_hash": &types.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression: aws.String(
			"SET #s = :status, finished_at = :now, expires_at = :ttl, http_status = :http_status, content_length = :content_length, content_type = :content_type, fetch_duration_ms = :duration, fetch_error = :error",
		),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status":         &types.AttributeValueMemberS{Value: finalStatus},
			":now":            &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
			":ttl":            &types.AttributeValueMemberN{Value: ttlStr},
			":http_status":    &types.AttributeValueMemberN{Value: strconv.Itoa(result.StatusCode)},
			":content_length": &types.AttributeValueMemberN{Value: strconv.FormatInt(result.ContentLength, 10)},
			":content_type":   &types.AttributeValueMemberS{Value: result.ContentType},
			":duration":       &types.AttributeValueMemberN{Value: strconv.FormatInt(result.DurationMs, 10)},
			":error":          &types.AttributeValueMemberS{Value: result.Error},
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

	return FetchResult{
		Success:       success,
		StatusCode:    resp.StatusCode,
		ContentLength: int64(len(body)),
		ContentType:   resp.Header.Get("Content-Type"),
		DurationMs:    time.Since(start).Milliseconds(),
		Error:         "",
	}
}

func main() {
	ctx := context.Background()

	crawler, err := NewCrawler(ctx)
	if err != nil {
		panic(err)
	}

	lambda.Start(crawler.Handler)
}
