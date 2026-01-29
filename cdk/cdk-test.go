package main

import (
	"fmt"
	"os"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudwatch"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudwatchactions"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambdaeventsources"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssns"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssqs"

	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

type CdkTestStackProps struct {
	awscdk.StackProps
	Stage string
}

func NewCdkTestStack(scope constructs.Construct, id string, props *CdkTestStackProps) awscdk.Stack {
	var sprops awscdk.StackProps
	if props != nil {
		sprops = props.StackProps
	}
	stack := awscdk.NewStack(scope, &id, &sprops)

	stage := "dev"
	if props != nil && props.Stage != "" {
		stage = props.Stage
	}

	// Tag all resources with stage for cost attribution
	awscdk.Tags_Of(stack).Add(jsii.String("Stage"), jsii.String(stage), nil)

	// The code that defines your stack goes here
	awss3.NewBucket(stack, jsii.String("SanityBucket"), &awss3.BucketProps{
		RemovalPolicy:     awscdk.RemovalPolicy_DESTROY,
		AutoDeleteObjects: jsii.Bool(true),
	})

	// Content storage bucket for crawled pages
	contentBucket := awss3.NewBucket(stack, jsii.String("ContentBucket"), &awss3.BucketProps{
		RemovalPolicy:     awscdk.RemovalPolicy_DESTROY,
		AutoDeleteObjects: jsii.Bool(true),
		LifecycleRules: &[]*awss3.LifecycleRule{
			{
				Expiration: awscdk.Duration_Days(jsii.Number(30)), // Auto-delete after 30 days
				Enabled:    jsii.Bool(true),
			},
		},
	})

	// Dead-letter queue
	dlq := awssqs.NewQueue(stack, jsii.String("UrlFrontierDLQ"), &awssqs.QueueProps{
		RetentionPeriod: awscdk.Duration_Days(jsii.Number(14)),
	})

	// Main URL frontier queue
	queue := awssqs.NewQueue(stack, jsii.String("UrlFrontierQueue"), &awssqs.QueueProps{
		VisibilityTimeout: awscdk.Duration_Seconds(jsii.Number(60)), // Must be >= Lambda timeout
		DeadLetterQueue: &awssqs.DeadLetterQueue{
			Queue:           dlq,
			MaxReceiveCount: jsii.Number(5),
		},
	})

	// URL state / dedup table
	table := awsdynamodb.NewTable(stack, jsii.String("UrlStateTable"), &awsdynamodb.TableProps{
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("url_hash"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		BillingMode:         awsdynamodb.BillingMode_PAY_PER_REQUEST,
		RemovalPolicy:       awscdk.RemovalPolicy_DESTROY,
		TimeToLiveAttribute: jsii.String("expires_at"),
	})

	// Lambda function for crawling
	crawlerLambda := awslambda.NewFunction(stack, jsii.String("CrawlerLambda"), &awslambda.FunctionProps{
		Runtime:                      awslambda.Runtime_PROVIDED_AL2023(),
		Handler:                      jsii.String("bootstrap"),
		Code:                         awslambda.Code_FromAsset(jsii.String("../lambda/bootstrap.zip"), nil),
		MemorySize:                   jsii.Number(128),
		Timeout:                      awscdk.Duration_Seconds(jsii.Number(30)),
		Architecture:                 awslambda.Architecture_ARM_64(),
		ReservedConcurrentExecutions: jsii.Number(10), // Cap concurrency to control costs and avoid account limits
		// Allow recursive loop: Lambda → SQS → Lambda is intentional for crawling
		RecursiveLoop: awslambda.RecursiveLoop_ALLOW,
		Environment: &map[string]*string{
			"TABLE_NAME":     table.TableName(),
			"QUEUE_URL":      queue.QueueUrl(),
			"CONTENT_BUCKET": contentBucket.BucketName(),
			"MAX_DEPTH":      jsii.String("3"),    // Limit crawl depth to prevent runaway costs
			"CRAWL_DELAY_MS": jsii.String("1000"), // 1 second delay between requests to same domain
		},
	})

	// Grant Lambda permissions
	table.GrantReadWriteData(crawlerLambda)
	queue.GrantSendMessages(crawlerLambda)     // Allow Lambda to enqueue discovered links
	contentBucket.GrantPut(crawlerLambda, "*") // Allow Lambda to upload content to S3

	// Add SQS trigger
	crawlerLambda.AddEventSource(awslambdaeventsources.NewSqsEventSource(queue, &awslambdaeventsources.SqsEventSourceProps{
		BatchSize:         jsii.Number(10),
		MaxBatchingWindow: awscdk.Duration_Seconds(jsii.Number(5)),
	}))

	// Tags
	awscdk.Tags_Of(queue).Add(jsii.String("Component"), jsii.String("crawler-frontier"), nil)
	awscdk.Tags_Of(queue).Add(jsii.String("Purpose"), jsii.String("url-ingestion"), nil)

	awscdk.Tags_Of(dlq).Add(jsii.String("Component"), jsii.String("crawler-frontier"), nil)
	awscdk.Tags_Of(dlq).Add(jsii.String("Purpose"), jsii.String("poison-messages"), nil)

	awscdk.Tags_Of(table).Add(jsii.String("Component"), jsii.String("crawler-frontier"), nil)
	awscdk.Tags_Of(table).Add(jsii.String("Purpose"), jsii.String("url-dedup-state"), nil)

	awscdk.Tags_Of(crawlerLambda).Add(jsii.String("Component"), jsii.String("crawler"), nil)
	awscdk.Tags_Of(crawlerLambda).Add(jsii.String("Purpose"), jsii.String("url-fetcher"), nil)

	// ========== MONITORING ==========

	// SNS Topic for alerts
	alertTopic := awssns.NewTopic(stack, jsii.String("CrawlerAlerts"), &awssns.TopicProps{
		DisplayName: jsii.String("Crawler Alerts"),
	})

	// CloudWatch Dashboard
	dashboardName := fmt.Sprintf("CrawlerDashboard-%s", stage)
	dashboard := awscloudwatch.NewDashboard(stack, jsii.String("CrawlerDashboard"), &awscloudwatch.DashboardProps{
		DashboardName: jsii.String(dashboardName),
	})

	// Lambda metrics
	lambdaInvocations := crawlerLambda.MetricInvocations(&awscloudwatch.MetricOptions{
		Period: awscdk.Duration_Minutes(jsii.Number(1)),
	})
	lambdaErrors := crawlerLambda.MetricErrors(&awscloudwatch.MetricOptions{
		Period: awscdk.Duration_Minutes(jsii.Number(1)),
	})
	lambdaDuration := crawlerLambda.MetricDuration(&awscloudwatch.MetricOptions{
		Period: awscdk.Duration_Minutes(jsii.Number(1)),
	})
	lambdaConcurrent := crawlerLambda.Metric(jsii.String("ConcurrentExecutions"), &awscloudwatch.MetricOptions{
		Period:    awscdk.Duration_Minutes(jsii.Number(1)),
		Statistic: jsii.String("Maximum"),
	})

	// SQS metrics
	queueMessages := queue.MetricApproximateNumberOfMessagesVisible(&awscloudwatch.MetricOptions{
		Period: awscdk.Duration_Minutes(jsii.Number(1)),
	})
	queueAge := queue.MetricApproximateAgeOfOldestMessage(&awscloudwatch.MetricOptions{
		Period: awscdk.Duration_Minutes(jsii.Number(1)),
	})
	dlqMessages := dlq.MetricApproximateNumberOfMessagesVisible(&awscloudwatch.MetricOptions{
		Period: awscdk.Duration_Minutes(jsii.Number(1)),
	})

	// Dashboard widgets
	dashboard.AddWidgets(
		// Row 1: Lambda overview
		awscloudwatch.NewGraphWidget(&awscloudwatch.GraphWidgetProps{
			Title:  jsii.String("Lambda Invocations & Errors"),
			Width:  jsii.Number(12),
			Height: jsii.Number(6),
			Left:   &[]awscloudwatch.IMetric{lambdaInvocations},
			Right:  &[]awscloudwatch.IMetric{lambdaErrors},
		}),
		awscloudwatch.NewGraphWidget(&awscloudwatch.GraphWidgetProps{
			Title:  jsii.String("Lambda Duration (ms)"),
			Width:  jsii.Number(6),
			Height: jsii.Number(6),
			Left:   &[]awscloudwatch.IMetric{lambdaDuration},
		}),
		awscloudwatch.NewGraphWidget(&awscloudwatch.GraphWidgetProps{
			Title:  jsii.String("Concurrent Executions"),
			Width:  jsii.Number(6),
			Height: jsii.Number(6),
			Left:   &[]awscloudwatch.IMetric{lambdaConcurrent},
		}),
	)

	dashboard.AddWidgets(
		// Row 2: Queue health
		awscloudwatch.NewGraphWidget(&awscloudwatch.GraphWidgetProps{
			Title:  jsii.String("Queue Depth"),
			Width:  jsii.Number(8),
			Height: jsii.Number(6),
			Left:   &[]awscloudwatch.IMetric{queueMessages},
		}),
		awscloudwatch.NewGraphWidget(&awscloudwatch.GraphWidgetProps{
			Title:  jsii.String("Message Age (seconds)"),
			Width:  jsii.Number(8),
			Height: jsii.Number(6),
			Left:   &[]awscloudwatch.IMetric{queueAge},
		}),
		awscloudwatch.NewGraphWidget(&awscloudwatch.GraphWidgetProps{
			Title:  jsii.String("Dead Letter Queue"),
			Width:  jsii.Number(8),
			Height: jsii.Number(6),
			Left:   &[]awscloudwatch.IMetric{dlqMessages},
		}),
	)

	// S3 metrics
	s3BucketSize := awscloudwatch.NewMetric(&awscloudwatch.MetricProps{
		Namespace:  jsii.String("AWS/S3"),
		MetricName: jsii.String("BucketSizeBytes"),
		DimensionsMap: &map[string]*string{
			"BucketName":  contentBucket.BucketName(),
			"StorageType": jsii.String("StandardStorage"),
		},
		Period:    awscdk.Duration_Days(jsii.Number(1)), // S3 metrics are daily
		Statistic: jsii.String("Average"),
	})
	s3ObjectCount := awscloudwatch.NewMetric(&awscloudwatch.MetricProps{
		Namespace:  jsii.String("AWS/S3"),
		MetricName: jsii.String("NumberOfObjects"),
		DimensionsMap: &map[string]*string{
			"BucketName":  contentBucket.BucketName(),
			"StorageType": jsii.String("AllStorageTypes"),
		},
		Period:    awscdk.Duration_Days(jsii.Number(1)),
		Statistic: jsii.String("Average"),
	})
	s3PutErrors := awscloudwatch.NewMetric(&awscloudwatch.MetricProps{
		Namespace:  jsii.String("AWS/S3"),
		MetricName: jsii.String("5xxErrors"),
		DimensionsMap: &map[string]*string{
			"BucketName": contentBucket.BucketName(),
			"FilterId":   jsii.String("AllRequests"),
		},
		Period:    awscdk.Duration_Minutes(jsii.Number(5)),
		Statistic: jsii.String("Sum"),
	})

	dashboard.AddWidgets(
		// Row 3: S3 storage
		awscloudwatch.NewGraphWidget(&awscloudwatch.GraphWidgetProps{
			Title:  jsii.String("S3 Bucket Size (bytes)"),
			Width:  jsii.Number(8),
			Height: jsii.Number(6),
			Left:   &[]awscloudwatch.IMetric{s3BucketSize},
		}),
		awscloudwatch.NewGraphWidget(&awscloudwatch.GraphWidgetProps{
			Title:  jsii.String("S3 Object Count"),
			Width:  jsii.Number(8),
			Height: jsii.Number(6),
			Left:   &[]awscloudwatch.IMetric{s3ObjectCount},
		}),
		awscloudwatch.NewGraphWidget(&awscloudwatch.GraphWidgetProps{
			Title:  jsii.String("S3 Put Errors (5xx)"),
			Width:  jsii.Number(8),
			Height: jsii.Number(6),
			Left:   &[]awscloudwatch.IMetric{s3PutErrors},
		}),
	)

	// Alarms
	// 1. Lambda errors alarm
	lambdaErrorsAlarm := awscloudwatch.NewAlarm(stack, jsii.String("LambdaErrorsAlarm"), &awscloudwatch.AlarmProps{
		AlarmDescription:   jsii.String("Crawler Lambda errors exceeded threshold"),
		Metric:             lambdaErrors,
		Threshold:          jsii.Number(5),
		EvaluationPeriods:  jsii.Number(2),
		ComparisonOperator: awscloudwatch.ComparisonOperator_GREATER_THAN_THRESHOLD,
		TreatMissingData:   awscloudwatch.TreatMissingData_NOT_BREACHING,
	})
	lambdaErrorsAlarm.AddAlarmAction(awscloudwatchactions.NewSnsAction(alertTopic))

	// 2. DLQ messages alarm (any message in DLQ is concerning)
	dlqAlarm := awscloudwatch.NewAlarm(stack, jsii.String("DLQAlarm"), &awscloudwatch.AlarmProps{
		AlarmDescription:   jsii.String("Messages in Dead Letter Queue"),
		Metric:             dlqMessages,
		Threshold:          jsii.Number(0),
		EvaluationPeriods:  jsii.Number(1),
		ComparisonOperator: awscloudwatch.ComparisonOperator_GREATER_THAN_THRESHOLD,
		TreatMissingData:   awscloudwatch.TreatMissingData_NOT_BREACHING,
	})
	dlqAlarm.AddAlarmAction(awscloudwatchactions.NewSnsAction(alertTopic))

	// 3. Lambda duration approaching timeout (>25s when timeout is 30s)
	durationAlarm := awscloudwatch.NewAlarm(stack, jsii.String("LambdaDurationAlarm"), &awscloudwatch.AlarmProps{
		AlarmDescription: jsii.String("Lambda duration approaching timeout"),
		Metric: crawlerLambda.MetricDuration(&awscloudwatch.MetricOptions{
			Period:    awscdk.Duration_Minutes(jsii.Number(5)),
			Statistic: jsii.String("p95"),
		}),
		Threshold:          jsii.Number(25000), // 25 seconds in ms
		EvaluationPeriods:  jsii.Number(2),
		ComparisonOperator: awscloudwatch.ComparisonOperator_GREATER_THAN_THRESHOLD,
		TreatMissingData:   awscloudwatch.TreatMissingData_NOT_BREACHING,
	})
	durationAlarm.AddAlarmAction(awscloudwatchactions.NewSnsAction(alertTopic))

	// Outputs
	awscdk.NewCfnOutput(stack, jsii.String("UrlFrontierQueueUrl"), &awscdk.CfnOutputProps{
		Value: queue.QueueUrl(),
	})

	awscdk.NewCfnOutput(stack, jsii.String("UrlFrontierDLQUrl"), &awscdk.CfnOutputProps{
		Value: dlq.QueueUrl(),
	})

	awscdk.NewCfnOutput(stack, jsii.String("UrlStateTableName"), &awscdk.CfnOutputProps{
		Value: table.TableName(),
	})

	awscdk.NewCfnOutput(stack, jsii.String("CrawlerLambdaName"), &awscdk.CfnOutputProps{
		Value: crawlerLambda.FunctionName(),
	})

	awscdk.NewCfnOutput(stack, jsii.String("DashboardName"), &awscdk.CfnOutputProps{
		Value: dashboard.DashboardName(),
	})

	awscdk.NewCfnOutput(stack, jsii.String("AlertTopicArn"), &awscdk.CfnOutputProps{
		Value: alertTopic.TopicArn(),
	})

	awscdk.NewCfnOutput(stack, jsii.String("ContentBucketName"), &awscdk.CfnOutputProps{
		Value: contentBucket.BucketName(),
	})

	return stack
}

func main() {
	defer jsii.Close()

	app := awscdk.NewApp(nil)

	stage := os.Getenv("STAGE")
	if stage == "" {
		stage = "dev"
	}
	stackName := fmt.Sprintf("CrawlerStack-%s", stage)

	NewCdkTestStack(app, stackName, &CdkTestStackProps{
		StackProps: awscdk.StackProps{
			Env: env(),
		},
		Stage: stage,
	})

	app.Synth(nil)
}

// env determines the AWS environment (account+region) in which our stack is to
// be deployed. For more information see: https://docs.aws.amazon.com/cdk/latest/guide/environments.html
func env() *awscdk.Environment {
	return &awscdk.Environment{
		Account: jsii.String(os.Getenv("CDK_DEFAULT_ACCOUNT")),
		Region:  jsii.String(os.Getenv("CDK_DEFAULT_REGION")),
	}
}
