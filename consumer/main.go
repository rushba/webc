package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
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

func setupLogger(format, level, workerID string) zerolog.Logger {
	// Parse log level
	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)

	var log zerolog.Logger

	if format == "json" {
		// JSON output for log files / production
		log = zerolog.New(os.Stdout).With().Timestamp().Str("worker_id", workerID).Logger()
	} else {
		// Colored console output for development
		output := zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "15:04:05",
		}
		log = zerolog.New(output).With().Timestamp().Str("worker_id", workerID).Logger()
	}

	return log
}

func main() {
	// Load .env file (silent fail if not present — allows real env vars)
	_ = godotenv.Load("../.env")

	continuous := flag.Bool("continuous", false, "Run continuously (poll loop)")
	fail := flag.Bool("fail", false, "Simulate failure")
	workerID := flag.String("worker-id", "", "Worker ID for traceability (default: random)")
	logFormat := flag.String("log-format", "console", "Log format: console (colored) or json")
	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")
	batchSize := flag.Int("batch-size", 1, "Number of messages to fetch per poll (1-10)")
	flag.Parse()

	// Validate batch size
	if *batchSize < 1 || *batchSize > 10 {
		*batchSize = 1
	}

	// Generate random worker ID if not provided
	if *workerID == "" {
		*workerID = generateWorkerID()
	}

	// Setup logger
	log := setupLogger(*logFormat, *logLevel, *workerID)

	queueURL := os.Getenv("QUEUE_URL")
	tableName := os.Getenv("TABLE_NAME")

	if queueURL == "" || tableName == "" {
		log.Fatal().Msg("QUEUE_URL and TABLE_NAME must be set")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		log.Info().Str("signal", sig.String()).Msg("Shutting down")
		cancel()
	}()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}

	sqsClient := sqs.NewFromConfig(cfg)
	ddb := dynamodb.NewFromConfig(cfg)

	if *continuous {
		log.Info().Int("batch_size", *batchSize).Msg("Starting continuous polling (Ctrl+C to stop)")
		runLoop(ctx, sqsClient, ddb, queueURL, tableName, *fail, *batchSize, log)
	} else {
		pollOnce(ctx, sqsClient, ddb, queueURL, tableName, *fail, *batchSize, log)
	}
}

func runLoop(ctx context.Context, sqsClient *sqs.Client, ddb *dynamodb.Client, queueURL, tableName string, simulateFail bool, batchSize int, log zerolog.Logger) {
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Stopped")
			return
		default:
		}

		pollOnce(ctx, sqsClient, ddb, queueURL, tableName, simulateFail, batchSize, log)
	}
}

func pollOnce(ctx context.Context, sqsClient *sqs.Client, ddb *dynamodb.Client, queueURL, tableName string, simulateFail bool, batchSize int, log zerolog.Logger) {
	out, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            &queueURL,
		MaxNumberOfMessages: int32(batchSize),
		WaitTimeSeconds:     20, // Long polling (max)
	})
	if err != nil {
		if ctx.Err() != nil {
			return // Shutdown requested
		}
		log.Error().Err(err).Msg("Poll error")
		return
	}

	if len(out.Messages) == 0 {
		log.Debug().Msg("No messages")
		return
	}

	log.Debug().Int("count", len(out.Messages)).Msg("Received batch")

	for _, msg := range out.Messages {
		processMessage(ctx, sqsClient, ddb, queueURL, tableName, msg, simulateFail, log)
	}
}

func processMessage(ctx context.Context, sqsClient *sqs.Client, ddb *dynamodb.Client, queueURL, tableName string, msg sqstypes.Message, simulateFail bool, log zerolog.Logger) {
	url := *msg.Body
	urlHash := hashURL(url)

	log.Info().Str("url", url).Msg("Received")

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
		log.Warn().Str("url", url).Msg("LOST race — already claimed by another consumer")
		ack(ctx, sqsClient, queueURL, msg.ReceiptHandle)
		return
	}

	log.Info().Str("url", url).Msg("WON race — claimed for processing")

	ttl := time.Now().Add(7 * 24 * time.Hour).Unix()
	ttlStr := strconv.FormatInt(ttl, 10)

	// Step 2: failure path
	if simulateFail {
		log.Warn().Str("url", url).Msg("Simulating failure")

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
				":ttl":    &types.AttributeValueMemberN{Value: ttlStr},
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
			":ttl":  &types.AttributeValueMemberN{Value: ttlStr},
		},
	})
	if err != nil {
		log.Fatal().Err(err).Str("url", url).Msg("Failed to mark as done")
	}

	ack(ctx, sqsClient, queueURL, msg.ReceiptHandle)

	log.Info().Str("url", url).Msg("Processed successfully")
}

func ack(ctx context.Context, client *sqs.Client, queueURL string, receipt *string) {
	_, _ = client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueURL,
		ReceiptHandle: receipt,
	})
}
