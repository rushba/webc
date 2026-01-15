package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

const (
	stateQueued     = "queued"
	stateProcessing = "processing"
	stateDone       = "done"
	stateFailed     = "failed"
)

func hashURL(u string) string {
	h := sha256.Sum256([]byte(u))
	return hex.EncodeToString(h[:])
}

func main() {
	fail := flag.Bool("fail", false, "Simulate failure (do not delete message)")
	flag.Parse()

	queueURL := os.Getenv("QUEUE_URL")
	if queueURL == "" {
		fmt.Println("Error: QUEUE_URL env var must be set")
		os.Exit(1)
	}

	tableName := os.Getenv("TABLE_NAME")
	if tableName == "" {
		fmt.Println("Error: TABLE_NAME env var must be set")
		os.Exit(1)
	}

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		panic(err)
	}

	client := sqs.NewFromConfig(cfg)
	out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            &queueURL,
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     10, // long polling
		VisibilityTimeout:   30,
	})
	if err != nil {
		panic(err)
	}

	if len(out.Messages) == 0 {
		fmt.Println("No messages")
		return
	}

	msg := out.Messages[0]
	url := *msg.Body
	fmt.Println("Received:", url)

	ddb := dynamodb.NewFromConfig(cfg)
	urlHash := hashURL(url)

	// Try to transition queued → processing
	_, err = ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &tableName,
		Key: map[string]types.AttributeValue{
			"url_hash": &types.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression:    aws.String("SET #s = :processing, processing_at = :now ADD attempts :one"),
		ConditionExpression: aws.String("#s = :queued"),

		ExpressionAttributeNames: map[string]string{"#s": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":queued":     &types.AttributeValueMemberS{Value: stateQueued},
			":processing": &types.AttributeValueMemberS{Value: stateProcessing},
			":now":        &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
			":one":        &types.AttributeValueMemberN{Value: "1"},
		},
	})

	if err != nil {
		fmt.Println("URL already processed or in progress, acking:", url)

		// Ack anyway — message is no longer useful
		_, _ = client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
			QueueUrl:      &queueURL,
			ReceiptHandle: msg.ReceiptHandle,
		})
		return
	}

	ttl := time.Now().Add(7 * 24 * time.Hour).Unix()

	_, err = ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &tableName,
		Key: map[string]types.AttributeValue{
			"url_hash": &types.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression: aws.String(
			"SET #s = :done, finished_at = :now, expires_at = :ttl",
		),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":done": &types.AttributeValueMemberS{Value: stateDone},
			":now":  &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
			":ttl":  &types.AttributeValueMemberN{Value: fmt.Sprint(ttl)},
		},
	})

	if *fail {
		fmt.Println("Simulating failure (message not deleted)")
		_, _ = ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: &tableName,
			Key: map[string]types.AttributeValue{
				"url_hash": &types.AttributeValueMemberS{Value: urlHash},
			},
			UpdateExpression: aws.String(
				"SET #s = :failed, finished_at = :now, expires_at = :ttl",
			),
			ExpressionAttributeNames: map[string]string{
				"#s": "status",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":failed": &types.AttributeValueMemberS{Value: stateFailed},
				":now":    &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
				":ttl":    &types.AttributeValueMemberN{Value: fmt.Sprint(ttl)},
			},
		})
		return
	}
}
