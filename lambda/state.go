package main

import (
	"context"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// claimURL attempts to transition URL from queued -> processing (returns true if won)
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

// saveFetchResult persists fetch metadata to DynamoDB
func (c *Crawler) saveFetchResult(ctx context.Context, urlHash string, result *FetchResult, depth int) error {
	status := stateDone
	if !result.Success {
		status = stateFailed
	}

	ttl := time.Now().Add(itemTTL).Unix()
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
