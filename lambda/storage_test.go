package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestIsHTML(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{"text/html", "text/html", true},
		{"text/html with charset", "text/html; charset=utf-8", true},
		{"application/xhtml", "application/xhtml+xml", true},
		{"text/plain", "text/plain", false},
		{"application/json", "application/json", false},
		{"image/png", "image/png", false},
		{"empty", "", false},
		{"case insensitive", "Text/HTML", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isHTML(tt.contentType)
			if got != tt.want {
				t.Errorf("isHTML(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

func TestGzipCompress(t *testing.T) {
	data := []byte("hello world")
	compressed, err := gzipCompress(data)
	if err != nil {
		t.Fatalf("gzipCompress() error = %v", err)
	}

	if len(compressed) == 0 {
		t.Error("gzipCompress() returned empty result")
	}

	// Compressed data should have gzip magic number
	if compressed[0] != 0x1f || compressed[1] != 0x8b {
		t.Error("gzipCompress() output missing gzip magic number")
	}

	// Empty input should work
	compressed, err = gzipCompress([]byte{})
	if err != nil {
		t.Fatalf("gzipCompress(empty) error = %v", err)
	}
	if len(compressed) == 0 {
		t.Error("gzipCompress(empty) returned empty result")
	}
}

func TestGzipCompressPooled(t *testing.T) {
	data := []byte("hello world from pooled compressor")
	compressed, err := gzipCompressPooled(data)
	if err != nil {
		t.Fatalf("gzipCompressPooled() error = %v", err)
	}

	if compressed[0] != 0x1f || compressed[1] != 0x8b {
		t.Error("gzipCompressPooled() output missing gzip magic number")
	}

	// Verify decompression matches original
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	decompressed, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to decompress: %v", err)
	}
	_ = r.Close()
	if !bytes.Equal(decompressed, data) {
		t.Error("decompressed data doesn't match original")
	}
}

func TestGzipCompressPooledConcurrent(t *testing.T) {
	// Verify pool safety under concurrent use
	data := []byte("concurrent test data")
	errs := make(chan error, 10)

	for range 10 {
		go func() {
			compressed, err := gzipCompressPooled(data)
			if err != nil {
				errs <- err
				return
			}
			r, err := gzip.NewReader(bytes.NewReader(compressed))
			if err != nil {
				errs <- err
				return
			}
			got, err := io.ReadAll(r)
			_ = r.Close()
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(got, data) {
				errs <- fmt.Errorf("data mismatch")
				return
			}
			errs <- nil
		}()
	}

	for range 10 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent gzipCompressPooled failed: %v", err)
		}
	}
}

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
