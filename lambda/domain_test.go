package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestIsDomainAllowed(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		getItem func(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
		want    bool
	}{
		{
			name: "active domain returns true",
			host: "example.com",
			getItem: func(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{
					Item: map[string]dynamodbtypes.AttributeValue{
						"status": &dynamodbtypes.AttributeValueMemberS{Value: "active"},
					},
				}, nil
			},
			want: true,
		},
		{
			name: "paused domain returns false",
			host: "example.com",
			getItem: func(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{
					Item: map[string]dynamodbtypes.AttributeValue{
						"status": &dynamodbtypes.AttributeValueMemberS{Value: "paused"},
					},
				}, nil
			},
			want: false,
		},
		{
			name: "missing domain returns false",
			host: "unknown.com",
			getItem: func(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{Item: nil}, nil
			},
			want: false,
		},
		{
			name: "DynamoDB error returns false",
			host: "example.com",
			getItem: func(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
				return nil, fmt.Errorf("DynamoDB error")
			},
			want: false,
		},
		{
			name: "wrong attribute type returns false",
			host: "example.com",
			getItem: func(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{
					Item: map[string]dynamodbtypes.AttributeValue{
						"status": &dynamodbtypes.AttributeValueMemberN{Value: "123"},
					},
				}, nil
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ddb := &mockDynamoDB{getItemFunc: tt.getItem}
			c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
			got := c.isDomainAllowed(context.Background(), tt.host)
			if got != tt.want {
				t.Errorf("isDomainAllowed(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestIsDomainAllowedChecksCorrectKey(t *testing.T) {
	var capturedKey string
	ddb := &mockDynamoDB{
		getItemFunc: func(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			if hash, ok := input.Key["url_hash"].(*dynamodbtypes.AttributeValueMemberS); ok {
				capturedKey = hash.Value
			}
			return &dynamodb.GetItemOutput{Item: nil}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	c.isDomainAllowed(context.Background(), "example.com")

	expected := "allowed_domain#example.com"
	if capturedKey != expected {
		t.Errorf("expected key %q, got %q", expected, capturedKey)
	}
}

func TestMaybeAddDomain(t *testing.T) {
	tests := []struct {
		name    string
		putItem func(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
		want    bool
	}{
		{
			name: "new domain added successfully",
			putItem: func(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
				return &dynamodb.PutItemOutput{}, nil
			},
			want: true,
		},
		{
			name: "domain already exists",
			putItem: func(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
				return nil, errConditionalCheckFailed
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ddb := &mockDynamoDB{putItemFunc: tt.putItem}
			c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
			got := c.maybeAddDomain(context.Background(), "new.com", "https://example.com")
			if got != tt.want {
				t.Errorf("maybeAddDomain() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMaybeAddDomainSetsCorrectAttributes(t *testing.T) {
	var capturedHost, capturedSource string
	ddb := &mockDynamoDB{
		putItemFunc: func(_ context.Context, input *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			if domain, ok := input.Item["domain"].(*dynamodbtypes.AttributeValueMemberS); ok {
				capturedHost = domain.Value
			}
			if from, ok := input.Item["discovered_from"].(*dynamodbtypes.AttributeValueMemberS); ok {
				capturedSource = from.Value
			}
			return &dynamodb.PutItemOutput{}, nil
		},
	}

	c := newTestCrawlerWithMocks(ddb, &mockSQS{}, &mockS3{})
	c.maybeAddDomain(context.Background(), "new.com", "https://example.com/page")

	if capturedHost != "new.com" {
		t.Errorf("expected domain new.com, got %q", capturedHost)
	}
	if capturedSource != "https://example.com/page" {
		t.Errorf("expected discovered_from https://example.com/page, got %q", capturedSource)
	}
}
