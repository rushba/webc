package main

import (
	"context"
	"fmt"
	"lambda/internal/parser"
	"lambda/internal/urls"
	"strconv"

	"github.com/aws/aws-lambda-go/events"
)

func (c *Crawler) Handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	c.log.Info().Int("count", len(sqsEvent.Records)).Msg("Received batch")

	for i := range sqsEvent.Records {
		if err := c.processMessage(ctx, &sqsEvent.Records[i]); err != nil {
			c.log.Error().Err(err).Str("message_id", sqsEvent.Records[i].MessageId).Msg("Failed to process message")
		}
	}

	return nil
}

func (c *Crawler) processMessage(ctx context.Context, record *events.SQSMessage) error {
	targetURL := record.Body
	urlHash := urls.Hash(targetURL)
	depth := c.extractDepth(record)

	c.log.Info().Str("url", targetURL).Int("depth", depth).Msg("Processing")

	if !c.claimURL(ctx, urlHash) {
		c.log.Warn().Str("url", targetURL).Msg("LOST race — already claimed")
		return nil
	}
	c.log.Info().Str("url", targetURL).Msg("WON race — checking robots.txt")

	if !c.isAllowedByRobots(ctx, targetURL) {
		c.log.Info().Str("url", targetURL).Msg("Blocked by robots.txt")
		return c.markStatus(ctx, urlHash, stateRobotsBlocked)
	}

	if !c.checkRateLimit(ctx, urls.GetDomain(targetURL)) {
		return c.handleRateLimited(ctx, targetURL, urlHash, depth)
	}

	result := c.fetchURL(ctx, targetURL)

	if !result.Success {
		// Classify the failure
		if result.StatusCode > 0 && isPermanentHTTPError(result.StatusCode) {
			// Permanent failure (404, 403, etc.) — save and acknowledge
			c.log.Warn().Str("url", targetURL).Int("status", result.StatusCode).Int64("ms", result.DurationMs).Msg("Permanent failure")
			return c.saveFetchResult(ctx, urlHash, &result, depth)
		}

		// Retriable failure (5xx, network error, etc.) — return error so SQS retries
		c.log.Warn().Str("url", targetURL).Int("status", result.StatusCode).Str("error", result.Error).Int64("ms", result.DurationMs).Msg("Retriable failure")
		return fmt.Errorf("retriable failure for %s: status=%d err=%s", targetURL, result.StatusCode, result.Error)
	}

	if err := c.saveFetchResult(ctx, urlHash, &result, depth); err != nil {
		return err
	}

	c.log.Info().Str("url", targetURL).Int("status", result.StatusCode).Int64("bytes", result.ContentLength).Int64("ms", result.DurationMs).Msg("Fetched successfully")
	c.processHTMLContent(ctx, targetURL, urlHash, &result, depth)
	return nil
}

// extractDepth gets crawl depth from SQS message attributes
func (c *Crawler) extractDepth(record *events.SQSMessage) int {
	if depthAttr, ok := record.MessageAttributes["depth"]; ok && depthAttr.StringValue != nil {
		if parsed, err := strconv.Atoi(*depthAttr.StringValue); err == nil {
			return parsed
		}
	}
	return 0
}

// processHTMLContent uploads content to S3 and extracts links.
// Uses single-pass HTML parsing to extract both text and links together.
func (c *Crawler) processHTMLContent(ctx context.Context, targetURL, urlHash string, result *FetchResult, depth int) {
	if !parser.IsHTML(result.ContentType) || len(result.Body) == 0 {
		return
	}

	// Single-pass parse: extract both text and links
	parsed := parser.Extract(result.Body, targetURL)

	// Upload to S3
	uploadResult, err := c.uploadContent(ctx, urlHash, result.Body, parsed.Text)
	if err != nil {
		c.log.Error().Err(err).Str("url", targetURL).Msg("Failed to upload content to S3")
	} else {
		c.saveS3Keys(ctx, targetURL, urlHash, uploadResult, len(parsed.Text))
	}

	// Enqueue discovered links
	if depth < c.maxDepth && len(parsed.Links) > 0 {
		c.log.Info().Str("url", targetURL).Int("links_found", len(parsed.Links)).Msg("Extracted links")
		enqueued := c.enqueueLinks(ctx, parsed.Links, depth+1, targetURL)
		if enqueued > 0 {
			c.log.Info().Str("url", targetURL).Int("enqueued", enqueued).Int("skipped", len(parsed.Links)-enqueued).Int("child_depth", depth+1).Msg("Enqueued new links")
		}
	}
}
