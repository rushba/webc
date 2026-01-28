package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/joho/godotenv"
)

func hashURL(u string) string {
	h := sha256.Sum256([]byte(u))
	return hex.EncodeToString(h[:])
}

func main() {
	_ = godotenv.Load("../.env")

	queueURL := os.Getenv("QUEUE_URL")
	tableName := os.Getenv("TABLE_NAME")

	if len(os.Args) < 2 {
		panic("usage: producer <url>")
	}
	url := os.Args[1]

	if url == "" || queueURL == "" || tableName == "" {
		panic("URL, QUEUE_URL, TABLE_NAME must be set")
	}

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		panic(err)
	}

	dynamo := dynamodb.NewFromConfig(cfg)
	sqsClient := sqs.NewFromConfig(cfg)

	urlHash := hashURL(url)
	fmt.Println("URL Hash:", urlHash)

	// 1) Dedup via conditional put
	_, err = dynamo.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &tableName,
		Item: map[string]types.AttributeValue{
			"url_hash": &types.AttributeValueMemberS{Value: urlHash},
			"url":      &types.AttributeValueMemberS{Value: url},
			"status":   &types.AttributeValueMemberS{Value: "queued"},
		},
		ConditionExpression: awsString("attribute_not_exists(url_hash)"),
	})
	if err != nil {
		fmt.Println("URL already seen, skipping:", url)
		return
	}

	// 2) Enqueue
	_, err = sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    &queueURL,
		MessageBody: &url,
	})
	if err != nil {
		panic(err)
	}

	fmt.Println("Enqueued URL:", url)
}

func awsString(s string) *string { return &s }
