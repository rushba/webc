package main

import (
	"context"
	"lambda/internal/urls"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// enqueueLinks adds new URLs to DynamoDB and SQS queue (with deduplication).
// Uses SQS SendMessageBatch to send up to 10 messages per API call.
func (c *Crawler) enqueueLinks(ctx context.Context, links []string, depth int, sourceURL string) int {
	enqueued := 0
	newDomains := 0
	depthStr := strconv.Itoa(depth)

	// Collect new URLs that pass dedup, then batch-send to SQS
	var pending []string

	for _, link := range links {
		host := urls.GetHost(link)
		if host == "" {
			continue
		}

		// Check if domain is allowed, auto-discover if not
		if !c.isDomainAllowed(ctx, host) {
			if c.maybeAddDomain(ctx, host, sourceURL) {
				newDomains++
			} else {
				continue
			}
		}

		urlHash := urls.Hash(link)

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
			continue
		}

		pending = append(pending, link)
	}

	// Batch send to SQS (up to 10 per batch)
	const sqsBatchSize = 10
	for i := 0; i < len(pending); i += sqsBatchSize {
		end := i + sqsBatchSize
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[i:end]

		entries := make([]sqstypes.SendMessageBatchRequestEntry, len(batch))
		for j, link := range batch {
			id := strconv.Itoa(i + j)
			linkCopy := link
			entries[j] = sqstypes.SendMessageBatchRequestEntry{
				Id:          &id,
				MessageBody: &linkCopy,
				MessageAttributes: map[string]sqstypes.MessageAttributeValue{
					"depth": {
						DataType:    aws.String("Number"),
						StringValue: &depthStr,
					},
				},
			}
		}

		result, err := c.sqs.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{
			QueueUrl: &c.queueURL,
			Entries:  entries,
		})
		if err != nil {
			c.log.Error().Err(err).Int("batch_size", len(batch)).Msg("Failed to batch-enqueue links")
			continue
		}

		enqueued += len(batch) - len(result.Failed)
		for _, fail := range result.Failed {
			c.log.Error().Str("id", *fail.Id).Str("code", *fail.Code).Msg("Failed to enqueue link in batch")
		}
	}

	if newDomains > 0 {
		c.log.Info().Int("new_domains", newDomains).Msg("Auto-discovered new domains")
	}

	return enqueued
}
