package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

func TestCheckRateLimitAllowed(t *testing.T) {
	ddb := &mockDynamoDB{
		putItemFunc: func(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	got := c.checkRateLimit(context.Background(), "example.com")
	if !got {
		t.Error("checkRateLimit() = false, want true")
	}
}

func TestCheckRateLimitBlocked(t *testing.T) {
	ddb := &mockDynamoDB{
		putItemFunc: func(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			return nil, errConditionalCheckFailed
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	got := c.checkRateLimit(context.Background(), "example.com")
	if got {
		t.Error("checkRateLimit() = true, want false")
	}
}

func TestCheckRateLimitDisabled(t *testing.T) {
	c := newTestCrawler()
	c.crawlDelayMs = 0

	// Should always return true when rate limiting is disabled
	got := c.checkRateLimit(context.Background(), "example.com")
	if !got {
		t.Error("checkRateLimit() = false, want true (disabled)")
	}
}

func TestCheckRateLimitNegativeDelay(t *testing.T) {
	c := newTestCrawler()
	c.crawlDelayMs = -1

	got := c.checkRateLimit(context.Background(), "example.com")
	if !got {
		t.Error("checkRateLimit() = false, want true (negative delay)")
	}
}

func TestHandleRateLimited(t *testing.T) {
	updateCalls := 0
	sqsSendCalls := 0

	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			updateCalls++
			return &dynamodb.UpdateItemOutput{}, nil
		},
	}

	sqsClient := &mockSQS{
		sendMessageFunc: func(_ context.Context, _ *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
			sqsSendCalls++
			return &sqs.SendMessageOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, sqsClient, &mockS3{})
	err := c.handleRateLimited(context.Background(), "https://example.com/page", "abc123", 1)
	if err != nil {
		t.Fatalf("handleRateLimited() error = %v", err)
	}

	// Should reset URL status to queued
	if updateCalls != 1 {
		t.Errorf("expected 1 UpdateItem call (reset to queued), got %d", updateCalls)
	}

	// Should re-enqueue with delay
	if sqsSendCalls != 1 {
		t.Errorf("expected 1 SendMessage call (requeue), got %d", sqsSendCalls)
	}
}

func TestHandleRateLimitedMinDelay(t *testing.T) {
	var capturedDelay int32
	sqsClient := &mockSQS{
		sendMessageFunc: func(_ context.Context, input *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
			capturedDelay = input.DelaySeconds
			return &sqs.SendMessageOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(&mockDynamoDB{}, sqsClient, &mockS3{})
	c.crawlDelayMs = 500 // Less than 1 second

	_ = c.handleRateLimited(context.Background(), "https://example.com/page", "abc123", 0)

	// Minimum delay should be 1 second
	if capturedDelay < 1 {
		t.Errorf("expected delay >= 1, got %d", capturedDelay)
	}
}

func TestRequeueWithDelay(t *testing.T) {
	var capturedDelay int32
	var capturedBody string
	sqsClient := &mockSQS{
		sendMessageFunc: func(_ context.Context, input *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
			capturedDelay = input.DelaySeconds
			capturedBody = *input.MessageBody
			return &sqs.SendMessageOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(&mockDynamoDB{}, sqsClient, &mockS3{})

	err := c.requeueWithDelay(context.Background(), "https://example.com", 2, 5)
	if err != nil {
		t.Fatalf("requeueWithDelay() error = %v", err)
	}

	if capturedDelay != 5 {
		t.Errorf("expected delay 5, got %d", capturedDelay)
	}
	if capturedBody != "https://example.com" {
		t.Errorf("expected body %q, got %q", "https://example.com", capturedBody)
	}
}

func TestRequeueWithDelayCapsAtMax(t *testing.T) {
	var capturedDelay int32
	sqsClient := &mockSQS{
		sendMessageFunc: func(_ context.Context, input *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
			capturedDelay = input.DelaySeconds
			return &sqs.SendMessageOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(&mockDynamoDB{}, sqsClient, &mockS3{})

	_ = c.requeueWithDelay(context.Background(), "https://example.com", 0, 99999)

	if capturedDelay != int32(sqsMaxDelaySeconds) {
		t.Errorf("expected delay capped at %d, got %d", sqsMaxDelaySeconds, capturedDelay)
	}
}

func TestRequeueWithDelayError(t *testing.T) {
	sqsClient := &mockSQS{
		sendMessageFunc: func(_ context.Context, _ *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
			return nil, fmt.Errorf("SQS error")
		},
	}

	c := newTestCrawlerWithMocks(&mockDynamoDB{}, sqsClient, &mockS3{})

	err := c.requeueWithDelay(context.Background(), "https://example.com", 0, 1)
	if err == nil {
		t.Fatal("requeueWithDelay() expected error, got nil")
	}
}
