package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestUploadContentSuccess(t *testing.T) {
	var uploadedKeys []string
	s3Client := &mockS3{
		putObjectFunc: func(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			uploadedKeys = append(uploadedKeys, *input.Key)
			if *input.Bucket != "test-bucket" {
				t.Errorf("expected bucket test-bucket, got %s", *input.Bucket)
			}
			return &s3.PutObjectOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(&mockDynamoDB{}, &mockSQS{}, s3Client)
	result, err := c.uploadContent(context.Background(), "abc123", []byte("<html>test</html>"), "test text")
	if err != nil {
		t.Fatalf("uploadContent() error = %v", err)
	}

	if result.RawKey != "abc123/raw.html.gz" {
		t.Errorf("expected raw key abc123/raw.html.gz, got %s", result.RawKey)
	}
	if result.TextKey != "abc123/text.txt.gz" {
		t.Errorf("expected text key abc123/text.txt.gz, got %s", result.TextKey)
	}
	if len(uploadedKeys) != 2 {
		t.Errorf("expected 2 S3 uploads, got %d", len(uploadedKeys))
	}
}

func TestUploadContentS3Error(t *testing.T) {
	s3Client := &mockS3{
		putObjectFunc: func(_ context.Context, _ *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			return nil, fmt.Errorf("S3 error")
		},
	}

	c := newTestCrawlerWithMocks(&mockDynamoDB{}, &mockSQS{}, s3Client)
	_, err := c.uploadContent(context.Background(), "abc123", []byte("<html>test</html>"), "test text")
	if err == nil {
		t.Fatal("uploadContent() expected error, got nil")
	}
}

func TestSaveS3Keys(t *testing.T) {
	var capturedUpdate *dynamodb.UpdateItemInput
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			capturedUpdate = input
			return &dynamodb.UpdateItemOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	upload := &UploadResult{RawKey: "hash/raw.html.gz", TextKey: "hash/text.txt.gz"}
	c.saveS3Keys(context.Background(), "https://example.com", "hash", upload, 100)

	if capturedUpdate == nil {
		t.Fatal("expected UpdateItem to be called")
	}
	if *capturedUpdate.TableName != "test-table" {
		t.Errorf("expected table test-table, got %s", *capturedUpdate.TableName)
	}
}

func TestSaveS3KeysError(t *testing.T) {
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			return nil, fmt.Errorf("DynamoDB error")
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	upload := &UploadResult{RawKey: "hash/raw.html.gz", TextKey: "hash/text.txt.gz"}

	// Should not panic, just log the error
	c.saveS3Keys(context.Background(), "https://example.com", "hash", upload, 100)
}
