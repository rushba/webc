package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load("../../.env")

	queue := flag.Bool("queue", false, "Purge SQS queue")
	table := flag.Bool("table", false, "Clear DynamoDB table")
	bucket := flag.Bool("bucket", false, "Clear S3 bucket")
	all := flag.Bool("all", false, "Purge queue, clear table, and clear bucket")
	flag.Parse()

	if !*queue && !*table && !*bucket && !*all {
		fmt.Println("Usage: cleanup [--queue] [--table] [--bucket] [--all]")
		fmt.Println("  --queue   Purge SQS queue")
		fmt.Println("  --table   Clear DynamoDB table")
		fmt.Println("  --bucket  Clear S3 bucket")
		fmt.Println("  --all     All of the above")
		os.Exit(1)
	}

	queueURL := os.Getenv("QUEUE_URL")
	tableName := os.Getenv("TABLE_NAME")
	bucketName := os.Getenv("CONTENT_BUCKET")

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		fmt.Println("Failed to load AWS config:", err)
		os.Exit(1)
	}

	if *queue || *all {
		if queueURL == "" {
			fmt.Println("QUEUE_URL not set, skipping queue")
		} else if err := purgeQueue(ctx, cfg, queueURL); err != nil {
			fmt.Println("Failed to purge queue:", err)
		} else {
			fmt.Println("✓ Queue purged")
		}
	}

	if *table || *all {
		if tableName == "" {
			fmt.Println("TABLE_NAME not set, skipping table")
		} else {
			count, err := clearTable(ctx, cfg, tableName)
			if err != nil {
				fmt.Println("Failed to clear table:", err)
			} else {
				fmt.Printf("✓ Table cleared (%d items deleted)\n", count)
			}
		}
	}

	if *bucket || *all {
		if bucketName == "" {
			fmt.Println("CONTENT_BUCKET not set, skipping bucket")
		} else {
			count, err := clearBucket(ctx, cfg, bucketName)
			if err != nil {
				fmt.Println("Failed to clear bucket:", err)
			} else {
				fmt.Printf("✓ Bucket cleared (%d objects deleted)\n", count)
			}
		}
	}
}

func purgeQueue(ctx context.Context, cfg aws.Config, queueURL string) error {
	client := sqs.NewFromConfig(cfg)

	_, err := client.PurgeQueue(ctx, &sqs.PurgeQueueInput{
		QueueUrl: &queueURL,
	})
	return err
}

func clearTable(ctx context.Context, cfg aws.Config, tableName string) (int, error) {
	client := dynamodb.NewFromConfig(cfg)

	// Scan all items
	var items []map[string]types.AttributeValue
	var lastKey map[string]types.AttributeValue

	for {
		out, err := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:            &tableName,
			ProjectionExpression: aws.String("url_hash"),
			ExclusiveStartKey:    lastKey,
		})
		if err != nil {
			return 0, err
		}

		items = append(items, out.Items...)

		if out.LastEvaluatedKey == nil {
			break
		}
		lastKey = out.LastEvaluatedKey
	}

	// Delete each item
	deleted := 0
	for _, item := range items {
		_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: &tableName,
			Key:       item,
		})
		if err != nil {
			fmt.Printf("Warning: failed to delete item: %v\n", err)
			continue
		}
		deleted++
	}

	return deleted, nil
}

func clearBucket(ctx context.Context, cfg aws.Config, bucketName string) (int, error) {
	client := s3.NewFromConfig(cfg)

	var objects []s3types.ObjectIdentifier
	var continuationToken *string

	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            &bucketName,
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return 0, err
		}

		for _, obj := range out.Contents {
			objects = append(objects, s3types.ObjectIdentifier{Key: obj.Key})
		}

		if !*out.IsTruncated {
			break
		}
		continuationToken = out.NextContinuationToken
	}

	if len(objects) == 0 {
		return 0, nil
	}

	// Delete in batches of 1000 (S3 limit)
	deleted := 0
	for i := 0; i < len(objects); i += 1000 {
		end := i + 1000
		if end > len(objects) {
			end = len(objects)
		}
		batch := objects[i:end]

		_, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: &bucketName,
			Delete: &s3types.Delete{Objects: batch},
		})
		if err != nil {
			return deleted, err
		}
		deleted += len(batch)
	}

	return deleted, nil
}
