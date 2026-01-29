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

// extractAndEnqueueLinks discovers and queues new URLs from HTML
func (c *Crawler) extractAndEnqueueLinks(ctx context.Context, targetURL string, body []byte, depth int) {
	if depth >= c.maxDepth {
		c.log.Info().Str("url", targetURL).Int("depth", depth).Int("max_depth", c.maxDepth).Msg("Max depth reached, not extracting links")
		return
	}

	links := extractLinks(body, targetURL)
	c.log.Info().Str("url", targetURL).Int("links_found", len(links)).Msg("Extracted links")

	enqueued := c.enqueueLinks(ctx, links, depth+1, targetURL)
	if enqueued > 0 {
		c.log.Info().Str("url", targetURL).Int("enqueued", enqueued).Int("skipped", len(links)-enqueued).Int("child_depth", depth+1).Msg("Enqueued new links")
	}
}

// enqueueLinks adds new URLs to DynamoDB and SQS queue (with deduplication)
// Auto-discovers new domains when external links are found
func (c *Crawler) enqueueLinks(ctx context.Context, links []string, depth int, sourceURL string) int {
	enqueued := 0
	newDomains := 0
	depthStr := strconv.Itoa(depth)

	for _, link := range links {
		host := getHost(link)
		if host == "" {
			continue
		}

		// Check if domain is allowed, auto-discover if not
		if !c.isDomainAllowed(ctx, host) {
			// Try to auto-add the domain
			if c.maybeAddDomain(ctx, host, sourceURL) {
				newDomains++
			} else {
				// Domain exists but is not active (paused/blocked) - skip
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
			// URL already exists - skip
			continue
		}

		// New URL - enqueue to SQS with depth attribute
		_, err = c.sqs.SendMessage(ctx, &sqs.SendMessageInput{
			QueueUrl:    &c.queueURL,
			MessageBody: &link,
			MessageAttributes: map[string]sqstypes.MessageAttributeValue{
				"depth": {
					DataType:    aws.String("Number"),
					StringValue: &depthStr,
				},
			},
		})
		if err != nil {
			c.log.Error().Err(err).Str("url", link).Msg("Failed to enqueue link")
			continue
		}

		enqueued++
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
