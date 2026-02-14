package main

import (
	"bytes"
	"context"
	"lambda/internal/compress"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"golang.org/x/sync/errgroup"
)

// UploadResult contains S3 keys for uploaded content
type UploadResult struct {
	RawKey  string
	TextKey string
}

// uploadContent uploads raw HTML and extracted text to S3 with gzip compression.
// Both uploads run concurrently via errgroup.
func (c *Crawler) uploadContent(ctx context.Context, urlHash string, rawHTML []byte, text string) (*UploadResult, error) {
	result := &UploadResult{
		RawKey:  urlHash + "/raw.html.gz",
		TextKey: urlHash + "/text.txt.gz",
	}

	g, ctx := errgroup.WithContext(ctx)

	// Upload raw HTML (gzip compressed) concurrently
	g.Go(func() error {
		rawGz, err := compress.Gzip(rawHTML)
		if err != nil {
			return err
		}
		_, err = c.s3.PutObject(ctx, &s3.PutObjectInput{
			Bucket:          &c.contentBucket,
			Key:             &result.RawKey,
			Body:            bytes.NewReader(rawGz),
			ContentType:     aws.String("text/html"),
			ContentEncoding: aws.String("gzip"),
		})
		return err
	})

	// Upload extracted text (gzip compressed) concurrently
	g.Go(func() error {
		textGz, err := compress.Gzip([]byte(text))
		if err != nil {
			return err
		}
		_, err = c.s3.PutObject(ctx, &s3.PutObjectInput{
			Bucket:          &c.contentBucket,
			Key:             &result.TextKey,
			Body:            bytes.NewReader(textGz),
			ContentType:     aws.String("text/plain"),
			ContentEncoding: aws.String("gzip"),
		})
		return err
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return result, nil
}

// saveS3Keys updates DynamoDB with S3 content locations
func (c *Crawler) saveS3Keys(ctx context.Context, targetURL, urlHash string, upload *UploadResult, textLen int) {
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &c.tableName,
		Key: map[string]dynamodbtypes.AttributeValue{
			"url_hash": &dynamodbtypes.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression: aws.String("SET s3_bucket = :bucket, s3_raw_key = :raw_key, s3_text_key = :text_key"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":bucket":   &dynamodbtypes.AttributeValueMemberS{Value: c.contentBucket},
			":raw_key":  &dynamodbtypes.AttributeValueMemberS{Value: upload.RawKey},
			":text_key": &dynamodbtypes.AttributeValueMemberS{Value: upload.TextKey},
		},
	})
	if err != nil {
		c.log.Error().Err(err).Str("url", targetURL).Msg("Failed to update DynamoDB with S3 keys")
		return
	}
	c.log.Info().Str("url", targetURL).Str("raw_key", upload.RawKey).Str("text_key", upload.TextKey).Int("text_len", textLen).Msg("Uploaded content to S3")
}
