package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/temoto/robotstxt"
)

func TestExtractDepth(t *testing.T) {
	c := newTestCrawler()

	tests := []struct {
		name   string
		record *events.SQSMessage
		want   int
	}{
		{
			name:   "no depth attribute",
			record: &events.SQSMessage{},
			want:   0,
		},
		{
			name: "depth 0",
			record: &events.SQSMessage{
				MessageAttributes: map[string]events.SQSMessageAttribute{
					"depth": {StringValue: aws.String("0")},
				},
			},
			want: 0,
		},
		{
			name: "depth 2",
			record: &events.SQSMessage{
				MessageAttributes: map[string]events.SQSMessageAttribute{
					"depth": {StringValue: aws.String("2")},
				},
			},
			want: 2,
		},
		{
			name: "invalid depth string",
			record: &events.SQSMessage{
				MessageAttributes: map[string]events.SQSMessageAttribute{
					"depth": {StringValue: aws.String("abc")},
				},
			},
			want: 0,
		},
		{
			name: "nil string value",
			record: &events.SQSMessage{
				MessageAttributes: map[string]events.SQSMessageAttribute{
					"depth": {},
				},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.extractDepth(tt.record)
			if got != tt.want {
				t.Errorf("extractDepth() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHandlerProcessesAllMessages(t *testing.T) {
	processed := 0
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			processed++
			// Fail claim so processMessage returns early
			return nil, errConditionalCheckFailed
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})

	event := events.SQSEvent{
		Records: []events.SQSMessage{
			{Body: "https://example.com/1", MessageId: "msg1"},
			{Body: "https://example.com/2", MessageId: "msg2"},
			{Body: "https://example.com/3", MessageId: "msg3"},
		},
	}

	err := c.Handler(context.Background(), event)
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}

	// Each message triggers one claimURL (UpdateItem) attempt
	if processed != 3 {
		t.Errorf("expected 3 UpdateItem calls, got %d", processed)
	}
}

func TestHandlerAlwaysReturnsNil(t *testing.T) {
	// Handler should always return nil (errors are logged, not propagated)
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			return nil, errConditionalCheckFailed
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})

	event := events.SQSEvent{
		Records: []events.SQSMessage{
			{Body: "https://example.com/1", MessageId: "msg1"},
		},
	}

	err := c.Handler(context.Background(), event)
	if err != nil {
		t.Fatalf("Handler() should always return nil, got: %v", err)
	}
}

func TestProcessMessageClaimLost(t *testing.T) {
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			return nil, errConditionalCheckFailed
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})

	record := &events.SQSMessage{Body: "https://example.com"}
	err := c.processMessage(context.Background(), record)
	if err != nil {
		t.Fatalf("processMessage() should return nil when claim lost, got: %v", err)
	}
}

func TestProcessMessageRobotsBlocked(t *testing.T) {
	updateCalls := 0
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			updateCalls++
			return &dynamodb.UpdateItemOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})

	// Pre-populate robots cache to block the URL
	robotsData, _ := robotstxt.FromString("User-agent: *\nDisallow: /blocked")
	c.robotsCache["https://example.com"] = robotsData

	record := &events.SQSMessage{Body: "https://example.com/blocked"}
	err := c.processMessage(context.Background(), record)
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}

	// Should have called UpdateItem twice: claimURL + markStatus
	if updateCalls != 2 {
		t.Errorf("expected 2 UpdateItem calls (claim + markStatus), got %d", updateCalls)
	}
}

func TestProcessMessageRetriableFailure(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			return &dynamodb.UpdateItemOutput{}, nil
		},
		putItemFunc: func(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	c.httpClient = testHTTPClientWith(handler)
	c.crawlDelayMs = 0

	record := &events.SQSMessage{Body: "https://example.com/page"}
	err := c.processMessage(context.Background(), record)
	if err == nil {
		t.Fatal("processMessage() should return error for retriable failure")
	}
}

func TestProcessMessagePermanentFailure(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	updateCalls := 0
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			updateCalls++
			return &dynamodb.UpdateItemOutput{}, nil
		},
		putItemFunc: func(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	c.httpClient = testHTTPClientWith(handler)
	c.crawlDelayMs = 0

	record := &events.SQSMessage{Body: "https://example.com/page"}
	err := c.processMessage(context.Background(), record)
	if err != nil {
		t.Fatalf("processMessage() should not return error for permanent failure, got: %v", err)
	}

	if updateCalls != 2 {
		t.Errorf("expected 2 UpdateItem calls, got %d", updateCalls)
	}
}

func TestProcessMessageSuccessfulFetch(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body><p>Hello</p><a href="/other">Link</a></body></html>`)
	})

	updateCalls := 0
	putCalls := 0
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			updateCalls++
			return &dynamodb.UpdateItemOutput{}, nil
		},
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

	s3Client := &mockS3{
		putObjectFunc: func(_ context.Context, _ *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			return &s3.PutObjectOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, s3Client)
	c.httpClient = testHTTPClientWith(handler)
	c.crawlDelayMs = 0

	record := &events.SQSMessage{Body: "https://example.com/page"}
	err := c.processMessage(context.Background(), record)
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}

	if updateCalls < 2 {
		t.Errorf("expected at least 2 UpdateItem calls, got %d", updateCalls)
	}
}

func TestProcessHTMLContentSkipsNonHTML(t *testing.T) {
	s3Calls := 0
	s3Client := &mockS3{
		putObjectFunc: func(_ context.Context, _ *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			s3Calls++
			return &s3.PutObjectOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(&mockDynamoDB{}, &mockSQS{}, s3Client)

	// JSON content type should be skipped
	result := &FetchResult{
		ContentType: "application/json",
		Body:        []byte(`{"key": "value"}`),
	}
	c.processHTMLContent(context.Background(), "https://example.com", "hash", result, 0)

	if s3Calls != 0 {
		t.Errorf("expected no S3 calls for non-HTML content, got %d", s3Calls)
	}

	// Empty body should also be skipped
	result = &FetchResult{
		ContentType: "text/html",
		Body:        []byte{},
	}
	c.processHTMLContent(context.Background(), "https://example.com", "hash", result, 0)

	if s3Calls != 0 {
		t.Errorf("expected no S3 calls for empty body, got %d", s3Calls)
	}
}

func TestProcessHTMLContentUploadsAndEnqueues(t *testing.T) {
	s3Calls := 0
	s3Client := &mockS3{
		putObjectFunc: func(_ context.Context, _ *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			s3Calls++
			return &s3.PutObjectOutput{}, nil
		},
	}

	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			return &dynamodb.UpdateItemOutput{}, nil
		},
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

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, s3Client)

	result := &FetchResult{
		ContentType: "text/html; charset=utf-8",
		Body:        []byte(`<html><body><p>Hello</p><a href="https://example.com/other">Link</a></body></html>`),
	}

	c.processHTMLContent(context.Background(), "https://example.com", "hash123", result, 0)

	// Should have uploaded raw HTML + extracted text = 2 S3 PutObject calls
	if s3Calls != 2 {
		t.Errorf("expected 2 S3 PutObject calls, got %d", s3Calls)
	}
}

func TestProcessHTMLContentAtMaxDepth(t *testing.T) {
	batchCalls := 0
	sqsClient := &mockSQS{
		sendMessageBatchFunc: func(_ context.Context, _ *sqs.SendMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error) {
			batchCalls++
			return &sqs.SendMessageBatchOutput{}, nil
		},
	}
	s3Client := &mockS3{
		putObjectFunc: func(_ context.Context, _ *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			return &s3.PutObjectOutput{}, nil
		},
	}
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			return &dynamodb.UpdateItemOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, sqsClient, s3Client)
	c.maxDepth = 2

	result := &FetchResult{
		ContentType: "text/html",
		Body:        []byte(`<html><body><a href="https://example.com/link">Link</a></body></html>`),
	}

	// At depth 2 with maxDepth 2, no links should be enqueued
	c.processHTMLContent(context.Background(), "https://example.com", "hash", result, 2)

	if batchCalls != 0 {
		t.Errorf("expected no SQS batch calls at max depth, got %d", batchCalls)
	}
}
