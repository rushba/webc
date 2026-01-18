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
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load("../../.env")

	queue := flag.Bool("queue", false, "Purge SQS queue")
	table := flag.Bool("table", false, "Clear DynamoDB table")
	all := flag.Bool("all", false, "Purge queue and clear table")
	flag.Parse()

	if !*queue && !*table && !*all {
		fmt.Println("Usage: cleanup [--queue] [--table] [--all]")
		fmt.Println("  --queue  Purge SQS queue")
		fmt.Println("  --table  Clear DynamoDB table")
		fmt.Println("  --all    Both queue and table")
		os.Exit(1)
	}

	queueURL := os.Getenv("QUEUE_URL")
	tableName := os.Getenv("TABLE_NAME")

	if queueURL == "" || tableName == "" {
		fmt.Println("QUEUE_URL and TABLE_NAME must be set")
		os.Exit(1)
	}

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		fmt.Println("Failed to load AWS config:", err)
		os.Exit(1)
	}

	if *queue || *all {
		if err := purgeQueue(ctx, cfg, queueURL); err != nil {
			fmt.Println("Failed to purge queue:", err)
		} else {
			fmt.Println("✓ Queue purged")
		}
	}

	if *table || *all {
		count, err := clearTable(ctx, cfg, tableName)
		if err != nil {
			fmt.Println("Failed to clear table:", err)
		} else {
			fmt.Printf("✓ Table cleared (%d items deleted)\n", count)
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
