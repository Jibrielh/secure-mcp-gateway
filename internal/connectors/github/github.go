package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(base, token string) (*Client, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Host == "" {
		return nil, errors.New("github base URL is not allowed")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost")) {
		return nil, errors.New("github base URL is not allowed")
	}
	host := parsed.Host
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Host != host || len(via) > 2 {
			return errors.New("redirect rejected")
		}
		return nil
	}}
	return &Client{BaseURL: strings.TrimRight(parsed.String(), "/"), Token: token, HTTP: client}, nil
}

type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	URL    string `json:"html_url"`
}

func (c *Client) ListIssues(ctx context.Context, owner, repo string, limit int) ([]Issue, error) {
	if !namePattern.MatchString(owner) || !namePattern.MatchString(repo) {
		return nil, errors.New("invalid repository")
	}
	limit = bound(limit)
	body, err := c.get(ctx, "/repos/"+owner+"/"+repo+"/issues?state=open&per_page="+strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	var issues []Issue
	if err := json.Unmarshal(body, &issues); err != nil {
		return nil, errors.New("github response was not understood")
	}
	if len(issues) > limit {
		issues = issues[:limit]
	}
	return issues, nil
}

func (c *Client) GetIssue(ctx context.Context, owner, repo string, number int) (Issue, error) {
	if !namePattern.MatchString(owner) || !namePattern.MatchString(repo) || number < 1 {
		return Issue{}, errors.New("invalid repository")
	}
	body, err := c.get(ctx, "/repos/"+owner+"/"+repo+"/issues/"+strconv.Itoa(number))
	if err != nil {
		return Issue{}, err
	}
	var issue Issue
	if err := json.Unmarshal(body, &issue); err != nil {
		return Issue{}, errors.New("github response was not understood")
	}
	return issue, nil
}

func (c *Client) GetFile(ctx context.Context, owner, repo, path, ref string) (string, error) {
	if !namePattern.MatchString(owner) || !namePattern.MatchString(repo) || strings.Contains(path, "..") || strings.HasPrefix(path, "/") {
		return "", errors.New("invalid repository path")
	}
	if ref == "" {
		ref = "HEAD"
	}
	if !namePattern.MatchString(ref) && ref != "HEAD" {
		return "", errors.New("invalid ref")
	}
	body, err := c.get(ctx, "/repos/"+owner+"/"+repo+"/contents/"+url.PathEscape(path)+"?ref="+url.QueryEscape(ref))
	if err != nil {
		return "", err
	}
	var payload struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Type != "file" {
		return "", errors.New("github response was not a file")
	}
	if len(payload.Content) > 64*1024 {
		return "", errors.New("file exceeds the size limit")
	}
	return payload.Content, nil
}

func (c *Client) SearchIssues(ctx context.Context, query string, limit int) ([]Issue, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 200 || strings.ContainsAny(query, "\r\n") {
		return nil, errors.New("invalid search")
	}
	limit = bound(limit)
	body, err := c.get(ctx, "/search/issues?per_page="+strconv.Itoa(limit)+"&q="+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	var payload struct {
		Items []Issue `json:"items"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("github response was not understood")
	}
	if len(payload.Items) > limit {
		payload.Items = payload.Items[:limit]
	}
	return payload.Items, nil
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agentgate-readonly")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("github request failed")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, errors.New("github request failed")
	}
	if res.StatusCode == 403 || res.StatusCode == 429 {
		return nil, errors.New("github rate limit reached")
	}
	if res.StatusCode != 200 {
		return nil, errors.New("github request failed")
	}
	return body, nil
}

func bound(limit int) int {
	if limit < 1 {
		return 10
	}
	if limit > 20 {
		return 20
	}
	return limit
}
