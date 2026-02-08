package main

import (
	"bytes"
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"golang.org/x/net/html"
)

// enqueueLinks adds new URLs to DynamoDB and SQS queue (with deduplication).
// Uses SQS SendMessageBatch to send up to 10 messages per API call.
func (c *Crawler) enqueueLinks(ctx context.Context, links []string, depth int, sourceURL string) int {
	enqueued := 0
	newDomains := 0
	depthStr := strconv.Itoa(depth)

	// Collect new URLs that pass dedup, then batch-send to SQS
	var pending []string

	for _, link := range links {
		host := getHost(link)
		if host == "" {
			continue
		}

		// Check if domain is allowed, auto-discover if not
		if !c.isDomainAllowed(ctx, host) {
			if c.maybeAddDomain(ctx, host, sourceURL) {
				newDomains++
			} else {
				continue
			}
		}

		urlHash := hashURL(link)

		// Try to add to DynamoDB (will fail if already exists)
		_, err := c.ddb.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: &c.tableName,
			Item: map[string]dynamodbtypes.AttributeValue{
				"url_hash": &dynamodbtypes.AttributeValueMemberS{Value: urlHash},
				"url":      &dynamodbtypes.AttributeValueMemberS{Value: link},
				"status":   &dynamodbtypes.AttributeValueMemberS{Value: stateQueued},
			},
			ConditionExpression: aws.String("attribute_not_exists(url_hash)"),
		})
		if err != nil {
			continue
		}

		pending = append(pending, link)
	}

	// Batch send to SQS (up to 10 per batch)
	const sqsBatchSize = 10
	for i := 0; i < len(pending); i += sqsBatchSize {
		end := i + sqsBatchSize
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[i:end]

		entries := make([]sqstypes.SendMessageBatchRequestEntry, len(batch))
		for j, link := range batch {
			id := strconv.Itoa(i + j)
			linkCopy := link
			entries[j] = sqstypes.SendMessageBatchRequestEntry{
				Id:          &id,
				MessageBody: &linkCopy,
				MessageAttributes: map[string]sqstypes.MessageAttributeValue{
					"depth": {
						DataType:    aws.String("Number"),
						StringValue: &depthStr,
					},
				},
			}
		}

		result, err := c.sqs.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{
			QueueUrl: &c.queueURL,
			Entries:  entries,
		})
		if err != nil {
			c.log.Error().Err(err).Int("batch_size", len(batch)).Msg("Failed to batch-enqueue links")
			continue
		}

		enqueued += len(batch) - len(result.Failed)
		for _, fail := range result.Failed {
			c.log.Error().Str("id", *fail.Id).Str("code", *fail.Code).Msg("Failed to enqueue link in batch")
		}
	}

	if newDomains > 0 {
		c.log.Info().Int("new_domains", newDomains).Msg("Auto-discovered new domains")
	}

	return enqueued
}

// extractLinks parses HTML and extracts all <a href> links, normalizing them to absolute URLs
func extractLinks(body []byte, baseURLStr string) []string {
	baseURL, err := url.Parse(baseURLStr)
	if err != nil {
		return nil
	}

	var links []string
	seen := make(map[string]bool)

	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil
	}

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					link := normalizeURL(attr.Val, baseURL)
					if link != "" && !seen[link] {
						seen[link] = true
						links = append(links, link)
					}
					break
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			traverse(child)
		}
	}
	traverse(doc)

	return links
}

// extractText parses HTML and extracts visible text content
func extractText(body []byte) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}

	var sb strings.Builder
	var extractNode func(*html.Node)
	extractNode = func(n *html.Node) {
		// Skip non-visible elements
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "head", "meta", "link":
				return
			}
		}

		// Extract text nodes
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				if sb.Len() > 0 {
					sb.WriteString(" ")
				}
				sb.WriteString(text)
			}
		}

		// Recurse into children
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			extractNode(child)
		}
	}
	extractNode(doc)

	return sb.String()
}

// ParseResult holds both extracted links and text from a single HTML parse pass.
type ParseResult struct {
	Links []string
	Text  string
}

// parseAndExtract parses HTML once, extracting both links and visible text in a single traversal.
// This avoids the double-parse cost of calling extractLinks + extractText separately.
func parseAndExtract(body []byte, baseURLStr string) ParseResult {
	baseURL, err := url.Parse(baseURLStr)
	if err != nil {
		return ParseResult{}
	}

	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ParseResult{}
	}

	var links []string
	seen := make(map[string]bool)
	var sb strings.Builder

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// Skip non-visible elements for text extraction
			switch n.Data {
			case "script", "style", "noscript", "head", "meta", "link":
				return
			}

			// Extract links from <a> elements
			if n.Data == "a" {
				for _, attr := range n.Attr {
					if attr.Key == "href" {
						link := normalizeURL(attr.Val, baseURL)
						if link != "" && !seen[link] {
							seen[link] = true
							links = append(links, link)
						}
						break
					}
				}
			}
		}

		// Extract text nodes
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				if sb.Len() > 0 {
					sb.WriteString(" ")
				}
				sb.WriteString(text)
			}
		}

		for child := n.FirstChild; child != nil; child = child.NextSibling {
			traverse(child)
		}
	}
	traverse(doc)

	return ParseResult{Links: links, Text: sb.String()}
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// normalizeURL converts a potentially relative URL to an absolute URL
// Returns empty string for URLs we don't want to crawl
func normalizeURL(href string, baseURL *url.URL) string {
	href = strings.TrimSpace(href)

	// Skip empty, fragments, javascript, mailto, tel, etc.
	if href == "" ||
		strings.HasPrefix(href, "#") ||
		strings.HasPrefix(href, "javascript:") ||
		strings.HasPrefix(href, "mailto:") ||
		strings.HasPrefix(href, "tel:") ||
		strings.HasPrefix(href, "data:") {
		return ""
	}

	// Parse the href
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}

	// Resolve relative URLs against base
	resolved := baseURL.ResolveReference(parsed)

	// Only keep http/https
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}

	// Remove fragment
	resolved.Fragment = ""

	// Note: Same-domain filter removed - domain allowlist checked in enqueueLinks()

	return resolved.String()
}
