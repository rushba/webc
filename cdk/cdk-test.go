package main

import (
	"os"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambdaeventsources"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssqs"

	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

type CdkTestStackProps struct {
	awscdk.StackProps
}

func NewCdkTestStack(scope constructs.Construct, id string, props *CdkTestStackProps) awscdk.Stack {
	var sprops awscdk.StackProps
	if props != nil {
		sprops = props.StackProps
	}
	stack := awscdk.NewStack(scope, &id, &sprops)

	// The code that defines your stack goes here
	awss3.NewBucket(stack, jsii.String("SanityBucket"), &awss3.BucketProps{
		RemovalPolicy:     awscdk.RemovalPolicy_DESTROY,
		AutoDeleteObjects: jsii.Bool(true),
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
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Handler:      jsii.String("bootstrap"),
		Code:         awslambda.Code_FromAsset(jsii.String("../lambda/bootstrap.zip"), nil),
		MemorySize:   jsii.Number(128),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(30)),
		Architecture: awslambda.Architecture_ARM_64(),
		// Allow recursive loop: Lambda → SQS → Lambda is intentional for crawling
		RecursiveLoop: awslambda.RecursiveLoop_ALLOW,
		Environment: &map[string]*string{
			"TABLE_NAME": table.TableName(),
			"QUEUE_URL":  queue.QueueUrl(),
			"MAX_DEPTH":  jsii.String("3"), // Limit crawl depth to prevent runaway costs
		},
	})

	// Grant Lambda permissions
	table.GrantReadWriteData(crawlerLambda)
	queue.GrantSendMessages(crawlerLambda) // Allow Lambda to enqueue discovered links

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

	return stack
}

func main() {
	defer jsii.Close()

	app := awscdk.NewApp(nil)

	NewCdkTestStack(app, "CdkTestStack", &CdkTestStackProps{
		awscdk.StackProps{
			Env: env(),
		},
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
