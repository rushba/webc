package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/rs/zerolog"
	"github.com/temoto/robotstxt"
)

func noopLogger() zerolog.Logger {
	return zerolog.New(io.Discard)
}

// testHTTPClient returns a plain http.Client without SSRF protection
func testHTTPClient() *http.Client {
	return &http.Client{}
}

// mockRoundTripper allows tests to intercept HTTP requests without a real server
type mockRoundTripper struct {
	handler http.Handler
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rr := httptest.NewRecorder()
	m.handler.ServeHTTP(rr, req)
	return rr.Result(), nil
}

// testHTTPClientWith returns an http.Client that routes requests through the given handler
// bypassing real network calls and SSRF checks on loopback
func testHTTPClientWith(handler http.Handler) *http.Client {
	return &http.Client{Transport: &mockRoundTripper{handler: handler}}
}

// mockDynamoDB implements DynamoDBAPI for testing
type mockDynamoDB struct {
	getItemFunc    func(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	putItemFunc    func(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	updateItemFunc func(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

func (m *mockDynamoDB) GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.getItemFunc != nil {
		return m.getItemFunc(ctx, params, optFns...)
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockDynamoDB) PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	if m.putItemFunc != nil {
		return m.putItemFunc(ctx, params, optFns...)
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (m *mockDynamoDB) UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	if m.updateItemFunc != nil {
		return m.updateItemFunc(ctx, params, optFns...)
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

// mockSQS implements SQSAPI for testing
type mockSQS struct {
	sendMessageFunc      func(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
	sendMessageBatchFunc func(ctx context.Context, params *sqs.SendMessageBatchInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error)
}

func (m *mockSQS) SendMessage(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	if m.sendMessageFunc != nil {
		return m.sendMessageFunc(ctx, params, optFns...)
	}
	return &sqs.SendMessageOutput{}, nil
}

func (m *mockSQS) SendMessageBatch(ctx context.Context, params *sqs.SendMessageBatchInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error) {
	if m.sendMessageBatchFunc != nil {
		return m.sendMessageBatchFunc(ctx, params, optFns...)
	}
	return &sqs.SendMessageBatchOutput{}, nil
}

// mockS3 implements S3API for testing
type mockS3 struct {
	putObjectFunc func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

func (m *mockS3) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if m.putObjectFunc != nil {
		return m.putObjectFunc(ctx, params, optFns...)
	}
	return &s3.PutObjectOutput{}, nil
}

// newTestCrawler creates a Crawler with mock dependencies for testing
func newTestCrawler() *Crawler {
	return newTestCrawlerWithMocks(&mockDynamoDB{}, &mockSQS{}, &mockS3{})
}

func newTestCrawlerWithMocks(ddb DynamoDBAPI, sqsClient SQSAPI, s3Client S3API) *Crawler {
	return &Crawler{
		ddb:           ddb,
		sqs:           sqsClient,
		s3:            s3Client,
		tableName:     "test-table",
		queueURL:      "https://sqs.us-east-1.amazonaws.com/123456789/test-queue",
		contentBucket: "test-bucket",
		maxDepth:      3,
		crawlDelayMs:  1000,
		log:           noopLogger(),
		robotsCache:   make(map[string]*robotstxt.RobotsData),
	}
}

// errConditionalCheckFailed simulates a DynamoDB conditional check failure
var errConditionalCheckFailed = fmt.Errorf("ConditionalCheckFailedException")
