package main

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// isDomainAllowed checks if a domain is in the allowed list
func (c *Crawler) isDomainAllowed(ctx context.Context, host string) bool {
	result, err := c.ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &c.tableName,
		Key: map[string]dynamodbtypes.AttributeValue{
			"url_hash": &dynamodbtypes.AttributeValueMemberS{Value: allowedDomainKeyPrefix + host},
		},
	})
	if err != nil || result.Item == nil {
		return false
	}
	statusAttr, ok := result.Item["status"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return false
	}
	return statusAttr.Value == domainStatusActive
}

// maybeAddDomain auto-discovers a new domain and adds it to the allowlist
// Returns true if domain was added (new), false if already exists
func (c *Crawler) maybeAddDomain(ctx context.Context, host, discoveredFrom string) bool {
	_, err := c.ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &c.tableName,
		Item: map[string]dynamodbtypes.AttributeValue{
			"url_hash":        &dynamodbtypes.AttributeValueMemberS{Value: allowedDomainKeyPrefix + host},
			"domain":          &dynamodbtypes.AttributeValueMemberS{Value: host},
			"status":          &dynamodbtypes.AttributeValueMemberS{Value: domainStatusActive},
			"discovered_from": &dynamodbtypes.AttributeValueMemberS{Value: discoveredFrom},
			"created_at":      &dynamodbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
		},
		ConditionExpression: aws.String("attribute_not_exists(url_hash)"),
	})
	if err != nil {
		return false // Already exists or error
	}
	c.log.Info().Str("domain", host).Str("discovered_from", discoveredFrom).Msg("Auto-discovered new domain")
	return true
}
