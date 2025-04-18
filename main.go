package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	// Create MCP server
	s := server.NewMCPServer(
		"Reddit API Tool 🔍",
		"1.0.0",
		server.WithLogging(),
		server.WithRecovery(),
	)

	// 1. Search Reddit Tool
	searchTool := mcp.NewTool("reddit_search",
		mcp.WithDescription("Search Reddit for posts matching a query"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query terms"),
		),
		mcp.WithString("subreddit",
			mcp.Description("Optional subreddit to search within (without the 'r/' prefix)"),
		),
		mcp.WithString("sort",
			mcp.Description("Sort method for results"),
			mcp.Enum("relevance", "hot", "new", "top"),
			mcp.DefaultString("relevance"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results to return (1-25)"),
			mcp.DefaultNumber(10),
			mcp.Min(1),
			mcp.Max(25),
		),
	)

	// 2. Get Post Details Tool
	postTool := mcp.NewTool("reddit_post",
		mcp.WithDescription("Get details for a specific Reddit post"),
		mcp.WithString("post_id",
			mcp.Required(),
			mcp.Description("Reddit post ID (with or without prefix)"),
		),
	)

	// 3. Get Comments Tool
	commentsTool := mcp.NewTool("reddit_comments",
		mcp.WithDescription("Get comments for a specific Reddit post"),
		mcp.WithString("post_id",
			mcp.Required(),
			mcp.Description("Reddit post ID (with or without prefix)"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of comments to return (1-100)"),
			mcp.DefaultNumber(25),
			mcp.Min(1),
			mcp.Max(100),
		),
		mcp.WithString("sort",
			mcp.Description("Sort method for comments"),
			mcp.Enum("top", "new", "controversial", "old", "qa"),
			mcp.DefaultString("top"),
		),
	)

	// Add tool handlers
	s.AddTool(searchTool, handleRedditSearch)
	s.AddTool(postTool, handleRedditPost)
	s.AddTool(commentsTool, handleRedditComments)

	// Start the server
	if err := server.ServeStdio(s); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
}

// Helper function to make Reddit API requests
func makeRedditRequest(endpoint string, params url.Values) (interface{}, error) {
	// Build the full URL
	baseURL := "https://www.reddit.com"
	requestURL := baseURL + endpoint

	if len(params) > 0 {
		requestURL += "?" + params.Encode()
	}

	// Create the HTTP request
	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set user-agent header to avoid rate limiting
	req.Header.Set("User-Agent", "mcp-reddit-tool/1.0")

	// Make the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned error status: %d", resp.StatusCode)
	}

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Try to parse as array first (for comments endpoint)
	var arrayResult []interface{}
	if err := json.Unmarshal(body, &arrayResult); err == nil {
		return arrayResult, nil
	}

	// If not an array, try as object
	var mapResult map[string]interface{}
	if err := json.Unmarshal(body, &mapResult); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	return mapResult, nil
}

// Handle Reddit search requests
func handleRedditSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract parameters
	query, ok := request.Params.Arguments["query"].(string)
	if !ok || query == "" {
		return mcp.NewToolResultError("search query is required"), nil
	}

	// Extract optional parameters
	params := url.Values{}
	params.Set("q", query)

	// Default limit
	limit := 10.0
	if limitParam, ok := request.Params.Arguments["limit"].(float64); ok {
		limit = limitParam
	}
	params.Set("limit", fmt.Sprintf("%d", int(limit)))

	// Default sort
	sort := "relevance"
	if sortParam, ok := request.Params.Arguments["sort"].(string); ok && sortParam != "" {
		sort = sortParam
	}
	params.Set("sort", sort)

	// Build endpoint path
	endpoint := "/search.json"
	if subreddit, ok := request.Params.Arguments["subreddit"].(string); ok && subreddit != "" {
		endpoint = fmt.Sprintf("/r/%s/search.json", subreddit)
	}

	// Make the API call
	result, err := makeRedditRequest(endpoint, params)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("Reddit API error", err), nil
	}

	// Format the response
	formattedResult, err := formatSearchResults(result)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("Failed to format results", err), nil
	}

	return mcp.NewToolResultText(formattedResult), nil
}

// Handle Reddit post details requests
func handleRedditPost(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract post ID
	postID, ok := request.Params.Arguments["post_id"].(string)
	if !ok || postID == "" {
		return mcp.NewToolResultError("post_id is required"), nil
	}

	// Clean the post ID if it includes the "t3_" prefix
	postID = strings.TrimPrefix(postID, "t3_")

	// Make the API call
	result, err := makeRedditRequest(fmt.Sprintf("/api/info.json"), url.Values{"id": []string{"t3_" + postID}})
	if err != nil {
		return mcp.NewToolResultErrorFromErr("Reddit API error", err), nil
	}

	// Format the response
	formattedResult, err := formatPostDetails(result)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("Failed to format post details", err), nil
	}

	return mcp.NewToolResultText(formattedResult), nil
}

// Handle Reddit comments requests
func handleRedditComments(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract post ID
	postID, ok := request.Params.Arguments["post_id"].(string)
	if !ok || postID == "" {
		return mcp.NewToolResultError("post_id is required"), nil
	}

	// Clean the post ID if it includes the "t3_" prefix
	postID = strings.TrimPrefix(postID, "t3_")

	// Extract optional parameters
	params := url.Values{}

	// Default limit
	limit := 25.0
	if limitParam, ok := request.Params.Arguments["limit"].(float64); ok {
		limit = limitParam
	}
	params.Set("limit", fmt.Sprintf("%d", int(limit)))

	// Default sort
	sort := "top"
	if sortParam, ok := request.Params.Arguments["sort"].(string); ok && sortParam != "" {
		sort = sortParam
	}
	params.Set("sort", sort)

	// Make the API call
	result, err := makeRedditRequest(fmt.Sprintf("/comments/%s.json", postID), params)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("Reddit API error", err), nil
	}

	// Format the response
	formattedResult, err := formatComments(result)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("Failed to format comments", err), nil
	}

	return mcp.NewToolResultText(formattedResult), nil
}

// Format search results into readable text
func formatSearchResults(data interface{}) (string, error) {
	// Cast to map for search results
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return "", errors.New("unexpected response format")
	}

	// Navigate to the posts in the data structure
	dataObject, ok := dataMap["data"].(map[string]interface{})
	if !ok {
		return "", errors.New("unexpected response format")
	}

	children, ok := dataObject["children"].([]interface{})
	if !ok {
		return "", errors.New("no results found")
	}

	if len(children) == 0 {
		return "No results found for this query.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d results:\n\n", len(children)))

	for i, child := range children {
		childData, ok := child.(map[string]interface{})["data"].(map[string]interface{})
		if !ok {
			continue
		}

		title := childData["title"].(string)
		author := childData["author"].(string)
		score := int(childData["score"].(float64))
		id := childData["id"].(string)

		sb.WriteString(fmt.Sprintf("%d. Title: %s\n", i+1, title))
		sb.WriteString(fmt.Sprintf("   Author: u/%s\n", author))
		sb.WriteString(fmt.Sprintf("   Score: %d\n", score))
		sb.WriteString(fmt.Sprintf("   Post ID: %s\n\n", id))
	}

	return sb.String(), nil
}

// Format post details into readable text
func formatPostDetails(data interface{}) (string, error) {
	// Cast to map for post details
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return "", errors.New("unexpected response format")
	}

	// Navigate to the post data
	dataObject, ok := dataMap["data"].(map[string]interface{})
	if !ok {
		return "", errors.New("unexpected response format")
	}

	children, ok := dataObject["children"].([]interface{})
	if !ok || len(children) == 0 {
		return "", errors.New("post not found")
	}

	postData, ok := children[0].(map[string]interface{})["data"].(map[string]interface{})
	if !ok {
		return "", errors.New("unexpected post data format")
	}

	var sb strings.Builder

	title := postData["title"].(string)
	author := postData["author"].(string)
	score := int(postData["score"].(float64))
	upvoteRatio := postData["upvote_ratio"].(float64)
	numComments := int(postData["num_comments"].(float64))
	created := int64(postData["created_utc"].(float64))

	sb.WriteString(fmt.Sprintf("Title: %s\n\n", title))
	sb.WriteString(fmt.Sprintf("Author: u/%s\n", author))
	sb.WriteString(fmt.Sprintf("Score: %d (%.0f%% upvoted)\n", score, upvoteRatio*100))
	sb.WriteString(fmt.Sprintf("Comments: %d\n", numComments))
	sb.WriteString(fmt.Sprintf("Created: %s\n\n", formatUnixTime(created)))

	// Post content
	if selftext, ok := postData["selftext"].(string); ok && selftext != "" {
		sb.WriteString(fmt.Sprintf("Content:\n%s\n\n", selftext))
	}

	// URL if it's a link post
	if url, ok := postData["url"].(string); ok && url != "" {
		if !strings.Contains(url, "reddit.com") {
			sb.WriteString(fmt.Sprintf("URL: %s\n\n", url))
		}
	}

	return sb.String(), nil
}

// Format comments into readable text
func formatComments(data interface{}) (string, error) {
	// Expect an array for comments
	resultList, ok := data.([]interface{})
	if !ok || len(resultList) < 2 {
		return "", errors.New("unexpected response format")
	}

	// Get the comments data
	commentsData, ok := resultList[1].(map[string]interface{})
	if !ok {
		return "", errors.New("comments data not found")
	}

	commentsObj, ok := commentsData["data"].(map[string]interface{})
	if !ok {
		return "", errors.New("comments object not found")
	}

	children, ok := commentsObj["children"].([]interface{})
	if !ok {
		return "", errors.New("no comments found")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d comments:\n\n", len(children)))

	// Process top-level comments
	for i, child := range children {
		childMap, ok := child.(map[string]interface{})
		if !ok {
			continue
		}

		// Skip "more" type entries
		kind, ok := childMap["kind"].(string)
		if !ok || kind == "more" {
			continue
		}

		childData, ok := childMap["data"].(map[string]interface{})
		if !ok {
			continue
		}

		author := childData["author"].(string)
		body := childData["body"].(string)
		score := int(childData["score"].(float64))

		sb.WriteString(fmt.Sprintf("%d. u/%s (%d points):\n", i+1, author, score))
		sb.WriteString(fmt.Sprintf("   %s\n\n", strings.ReplaceAll(body, "\n", "\n   ")))
	}

	return sb.String(), nil
}

// Helper function to format Unix timestamp
func formatUnixTime(timestamp int64) string {
	// In a real implementation, use time.Unix() to format the time
	// For simplicity, we'll just return the timestamp as a string
	return fmt.Sprintf("timestamp: %d", timestamp)
}
