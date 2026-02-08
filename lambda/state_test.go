package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestClaimURLSuccess(t *testing.T) {
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			if *input.TableName != "test-table" {
				t.Errorf("expected table test-table, got %s", *input.TableName)
			}
			return &dynamodb.UpdateItemOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	got := c.claimURL(context.Background(), "abc123")
	if !got {
		t.Error("claimURL() = false, want true")
	}
}

func TestClaimURLLostRace(t *testing.T) {
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			return nil, errConditionalCheckFailed
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	got := c.claimURL(context.Background(), "abc123")
	if got {
		t.Error("claimURL() = true, want false (race lost)")
	}
}

func TestMarkStatusSuccess(t *testing.T) {
	var capturedStatus string
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			for k, v := range input.ExpressionAttributeValues {
				if k == ":status" {
					if s, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
						capturedStatus = s.Value
					}
				}
			}
			return &dynamodb.UpdateItemOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	err := c.markStatus(context.Background(), "abc123", stateRobotsBlocked)
	if err != nil {
		t.Fatalf("markStatus() error = %v", err)
	}
	if capturedStatus != stateRobotsBlocked {
		t.Errorf("markStatus() captured status = %q, want %q", capturedStatus, stateRobotsBlocked)
	}
}

func TestMarkStatusError(t *testing.T) {
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			return nil, fmt.Errorf("DynamoDB error")
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	err := c.markStatus(context.Background(), "abc123", stateFailed)
	if err == nil {
		t.Fatal("markStatus() expected error, got nil")
	}
}

func TestSaveFetchResultSuccess(t *testing.T) {
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			if *input.TableName != "test-table" {
				t.Errorf("expected table test-table, got %s", *input.TableName)
			}
			return &dynamodb.UpdateItemOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	result := &FetchResult{
		Success:       true,
		StatusCode:    200,
		ContentLength: 1024,
		ContentType:   "text/html",
		DurationMs:    100,
	}

	err := c.saveFetchResult(context.Background(), "abc123", result, 1)
	if err != nil {
		t.Fatalf("saveFetchResult() error = %v", err)
	}
}

func TestSaveFetchResultFailedStatus(t *testing.T) {
	var capturedStatus string
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			for k, v := range input.ExpressionAttributeValues {
				if k == ":status" {
					if s, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
						capturedStatus = s.Value
					}
				}
			}
			return &dynamodb.UpdateItemOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	result := &FetchResult{
		Success:    false,
		StatusCode: 404,
		Error:      "not found",
	}

	err := c.saveFetchResult(context.Background(), "abc123", result, 0)
	if err != nil {
		t.Fatalf("saveFetchResult() error = %v", err)
	}
	if capturedStatus != stateFailed {
		t.Errorf("expected status %q, got %q", stateFailed, capturedStatus)
	}
}

func TestSaveFetchResultDynamoError(t *testing.T) {
	ddb := &mockDynamoDB{
		updateItemFunc: func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			return nil, fmt.Errorf("DynamoDB error")
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	result := &FetchResult{Success: true, StatusCode: 200}

	err := c.saveFetchResult(context.Background(), "abc123", result, 0)
	if err == nil {
		t.Fatal("saveFetchResult() expected error, got nil")
	}
}
