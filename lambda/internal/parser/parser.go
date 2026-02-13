package parser

import (
	"bytes"
	"lambda/internal/urls"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

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
					link := urls.Normalize(attr.Val, baseURL)
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

// Result holds both extracted links and text from a single HTML parse pass.
type Result struct {
	Links []string
	Text  string
}

// Extract parses HTML once, extracting both links and visible text in a single traversal.
// This avoids the double-parse cost of calling extractLinks + extractText separately.
func Extract(body []byte, baseURLStr string) Result {
	baseURL, err := url.Parse(baseURLStr)
	if err != nil {
		return Result{}
	}

	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return Result{}
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
						link := urls.Normalize(attr.Val, baseURL)
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

	return Result{Links: links, Text: sb.String()}
}
