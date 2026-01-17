package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/joho/godotenv"
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

func generateWorkerID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

func main() {
	// Load .env file (silent fail if not present — allows real env vars)
	_ = godotenv.Load("../.env")

	continuous := flag.Bool("continuous", false, "Run continuously (poll loop)")
	fail := flag.Bool("fail", false, "Simulate failure")
	workerID := flag.String("worker-id", "", "Worker ID for traceability (default: random)")
	flag.Parse()

	// Generate random worker ID if not provided
	if *workerID == "" {
		*workerID = generateWorkerID()
	}

	queueURL := os.Getenv("QUEUE_URL")
	tableName := os.Getenv("TABLE_NAME")

	if queueURL == "" || tableName == "" {
		fmt.Println("QUEUE_URL and TABLE_NAME must be set")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		fmt.Printf("\n[%s] Received %v, shutting down...\n", *workerID, sig)
		cancel()
	}()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		panic(err)
	}

	sqsClient := sqs.NewFromConfig(cfg)
	ddb := dynamodb.NewFromConfig(cfg)

	if *continuous {
		fmt.Printf("[%s] Starting continuous polling (Ctrl+C to stop)...\n", *workerID)
		runLoop(ctx, sqsClient, ddb, queueURL, tableName, *fail, *workerID)
	} else {
		pollOnce(ctx, sqsClient, ddb, queueURL, tableName, *fail, *workerID)
	}
}

func runLoop(ctx context.Context, sqsClient *sqs.Client, ddb *dynamodb.Client, queueURL, tableName string, simulateFail bool, workerID string) {
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("[%s] Stopped.\n", workerID)
			return
		default:
		}

		pollOnce(ctx, sqsClient, ddb, queueURL, tableName, simulateFail, workerID)
	}
}

func pollOnce(ctx context.Context, sqsClient *sqs.Client, ddb *dynamodb.Client, queueURL, tableName string, simulateFail bool, workerID string) {
	out, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            &queueURL,
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     20, // Long polling (max)
	})
	if err != nil {
		if ctx.Err() != nil {
			return // Shutdown requested
		}
		fmt.Printf("[%s] Poll error: %v\n", workerID, err)
		return
	}

	if len(out.Messages) == 0 {
		fmt.Printf("[%s] No messages\n", workerID)
		return
	}

	msg := out.Messages[0]
	processMessage(ctx, sqsClient, ddb, queueURL, tableName, msg, simulateFail, workerID)
}

func processMessage(ctx context.Context, sqsClient *sqs.Client, ddb *dynamodb.Client, queueURL, tableName string, msg sqstypes.Message, simulateFail bool, workerID string) {
	url := *msg.Body
	urlHash := hashURL(url)

	fmt.Printf("[%s] Received: %s\n", workerID, url)

	// Step 1: queued → processing (idempotent gate)
	_, err := ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &tableName,
		Key: map[string]types.AttributeValue{
			"url_hash": &types.AttributeValueMemberS{Value: urlHash},
		},
		UpdateExpression: aws.String(
			"SET #s = :processing, processing_at = :now ADD attempts :one",
		),
		ConditionExpression: aws.String("#s = :queued"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":queued":     &types.AttributeValueMemberS{Value: stateQueued},
			":processing": &types.AttributeValueMemberS{Value: stateProcessing},
			":now":        &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
			":one":        &types.AttributeValueMemberN{Value: "1"},
		},
	})

	if err != nil {
		fmt.Printf("[%s] LOST race — already claimed by another consumer: %s\n", workerID, url)
		ack(ctx, sqsClient, queueURL, msg.ReceiptHandle)
		return
	}

	fmt.Printf("[%s] WON race — claimed for processing: %s\n", workerID, url)

	ttl := time.Now().Add(7 * 24 * time.Hour).Unix()

	// Step 2: failure path
	if simulateFail {
		fmt.Printf("[%s] Simulating failure\n", workerID)

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

		ack(ctx, sqsClient, queueURL, msg.ReceiptHandle)
		return
	}

	// Simulated work
	time.Sleep(5 * time.Second)

	// Step 3: success → done
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
	if err != nil {
		panic(err)
	}

	ack(ctx, sqsClient, queueURL, msg.ReceiptHandle)

	fmt.Printf("[%s] Processed successfully: %s\n", workerID, url)
}

func ack(ctx context.Context, client *sqs.Client, queueURL string, receipt *string) {
	_, _ = client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueURL,
		ReceiptHandle: receipt,
	})
}
