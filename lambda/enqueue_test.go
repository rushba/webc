package main

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func TestEnqueueLinksSuccess(t *testing.T) {
	putCalls := 0
	batchCalls := 0
	var batchEntries int

	ddb := &mockDynamoDB{
		putItemFunc: func(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			putCalls++
			return &dynamodb.PutItemOutput{}, nil
		},
		getItemFunc: func(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{
				Item: map[string]dynamodbtypes.AttributeValue{
					"status": &dynamodbtypes.AttributeValueMemberS{Value: "active"},
				},
			}, nil
		},
	}

	sqsClient := &mockSQS{
		sendMessageBatchFunc: func(_ context.Context, input *sqs.SendMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error) {
			batchCalls++
			batchEntries += len(input.Entries)
			return &sqs.SendMessageBatchOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, sqsClient, &mockS3{})
	links := []string{
		"https://example.com/a",
		"https://example.com/b",
		"https://example.com/c",
	}

	enqueued := c.enqueueLinks(context.Background(), links, 1, "https://example.com")
	if enqueued != 3 {
		t.Errorf("enqueueLinks() = %d, want 3", enqueued)
	}
	if putCalls != 3 {
		t.Errorf("expected 3 PutItem calls, got %d", putCalls)
	}
	if batchCalls != 1 {
		t.Errorf("expected 1 batch send, got %d", batchCalls)
	}
	if batchEntries != 3 {
		t.Errorf("expected 3 entries in batch, got %d", batchEntries)
	}
}

func TestEnqueueLinksBatchesOver10(t *testing.T) {
	batchCalls := 0

	ddb := &mockDynamoDB{
		putItemFunc: func(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
		getItemFunc: func(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{
				Item: map[string]dynamodbtypes.AttributeValue{
					"status": &dynamodbtypes.AttributeValueMemberS{Value: "active"},
				},
			}, nil
		},
	}

	sqsClient := &mockSQS{
		sendMessageBatchFunc: func(_ context.Context, input *sqs.SendMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error) {
			batchCalls++
			if len(input.Entries) > 10 {
				t.Errorf("batch size %d exceeds SQS max of 10", len(input.Entries))
			}
			return &sqs.SendMessageBatchOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, sqsClient, &mockS3{})

	// Create 25 links - should produce 3 batches (10 + 10 + 5)
	links := make([]string, 25)
	for i := range links {
		links[i] = "https://example.com/page-" + string(rune('a'+i))
	}

	enqueued := c.enqueueLinks(context.Background(), links, 1, "https://example.com")
	if enqueued != 25 {
		t.Errorf("enqueueLinks() = %d, want 25", enqueued)
	}
	if batchCalls != 3 {
		t.Errorf("expected 3 batch calls (10+10+5), got %d", batchCalls)
	}
}

func TestEnqueueLinksDedup(t *testing.T) {
	putCalls := 0
	ddb := &mockDynamoDB{
		putItemFunc: func(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			putCalls++
			if putCalls > 1 {
				// Second and subsequent puts fail (already exists)
				return nil, errConditionalCheckFailed
			}
			return &dynamodb.PutItemOutput{}, nil
		},
		getItemFunc: func(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{
				Item: map[string]dynamodbtypes.AttributeValue{
					"status": &dynamodbtypes.AttributeValueMemberS{Value: "active"},
				},
			}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	links := []string{
		"https://example.com/new",
		"https://example.com/existing",
	}

	enqueued := c.enqueueLinks(context.Background(), links, 1, "https://example.com")
	if enqueued != 1 {
		t.Errorf("enqueueLinks() = %d, want 1 (one deduped)", enqueued)
	}
}

func TestEnqueueLinksEmptyHost(t *testing.T) {
	c := newTestCrawler()
	links := []string{"", "://invalid"}

	enqueued := c.enqueueLinks(context.Background(), links, 1, "https://example.com")
	if enqueued != 0 {
		t.Errorf("enqueueLinks() = %d, want 0 (invalid links)", enqueued)
	}
}

func TestEnqueueLinksDomainBlocked(t *testing.T) {
	ddb := &mockDynamoDB{
		getItemFunc: func(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			// Domain not found
			return &dynamodb.GetItemOutput{Item: nil}, nil
		},
		putItemFunc: func(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			// Domain already exists (blocked)
			return nil, errConditionalCheckFailed
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	links := []string{"https://blocked.com/page"}

	enqueued := c.enqueueLinks(context.Background(), links, 1, "https://example.com")
	if enqueued != 0 {
		t.Errorf("enqueueLinks() = %d, want 0 (domain blocked)", enqueued)
	}
}

func TestEnqueueLinksBatchPartialFailure(t *testing.T) {
	ddb := &mockDynamoDB{
		putItemFunc: func(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
		getItemFunc: func(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{
				Item: map[string]dynamodbtypes.AttributeValue{
					"status": &dynamodbtypes.AttributeValueMemberS{Value: "active"},
				},
			}, nil
		},
	}

	code := "InternalError"
	id := "1"
	sqsClient := &mockSQS{
		sendMessageBatchFunc: func(_ context.Context, input *sqs.SendMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error) {
			// Simulate 1 failure in the batch
			return &sqs.SendMessageBatchOutput{
				Failed: []sqstypes.BatchResultErrorEntry{
					{Id: &id, Code: &code, SenderFault: false},
				},
			}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, sqsClient, &mockS3{})
	links := []string{
		"https://example.com/a",
		"https://example.com/b",
		"https://example.com/c",
	}

	enqueued := c.enqueueLinks(context.Background(), links, 1, "https://example.com")
	// 3 in batch, 1 failed = 2 enqueued
	if enqueued != 2 {
		t.Errorf("enqueueLinks() = %d, want 2 (one batch failure)", enqueued)
	}
}
