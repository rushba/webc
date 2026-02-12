package main

import (
	"context"
	"lambda/internal/urls"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

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

// handleRateLimited resets URL to queued and re-queues with delay
func (c *Crawler) handleRateLimited(ctx context.Context, targetURL, urlHash string, depth int) error {
	c.log.Info().Str("url", targetURL).Str("domain", urls.GetDomain(targetURL)).Msg("Rate limited, re-queuing")

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

// requeueWithDelay sends the URL back to the queue with a delay
func (c *Crawler) requeueWithDelay(ctx context.Context, urlStr string, depth, delaySeconds int) error {
	depthStr := strconv.Itoa(depth)

	// Cap delay at SQS maximum
	if delaySeconds > sqsMaxDelaySeconds {
		delaySeconds = sqsMaxDelaySeconds
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
