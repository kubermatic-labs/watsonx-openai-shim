/*
Copyright 2026 The Kubermatic Kubernetes Platform contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package watsonx is a client for the watsonx.ai chat API.
package watsonx

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AuthMode selects how the client obtains bearer tokens.
type AuthMode string

const (
	// AuthModeIAM exchanges the API key at IBM Cloud IAM.
	AuthModeIAM AuthMode = "iam"
	// AuthModeCPD exchanges username and API key at a Cloud Pak for Data instance.
	AuthModeCPD AuthMode = "cpd"
)

const (
	chatPath       = "/ml/v1/text/chat"
	chatStreamPath = "/ml/v1/text/chat_stream"

	// versionParam is the query parameter watsonx selects the API version with.
	versionParam = "version"

	// chatAPIVersion is the version query parameter of the chat endpoints.
	chatAPIVersion = "2023-10-25"

	// maxErrorBodyBytes caps how much of an error response is read.
	maxErrorBodyBytes = 64 << 10
)

// Config describes the watsonx instance and the credentials to use.
type Config struct {
	// URL is the watsonx base URL, e.g. https://eu-de.ml.cloud.ibm.com or the CPD URL.
	URL       string
	ProjectID string

	AuthMode AuthMode
	// IAMHost is the IBM Cloud IAM host, used in IAM mode only.
	IAMHost  string
	Username string
	APIKey   string

	TLSConfig             *tls.Config
	ConnectTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
}

// Client calls the watsonx chat API.
// It is safe for concurrent use.
type Client struct {
	baseURL    *url.URL
	projectID  string
	httpClient *http.Client
	tokens     *tokenCache
	// isCPD enables features only a Cloud Pak for Data instance offers.
	isCPD bool
}

// NewClient creates a client.
// It obtains the first token with the first request.
func NewClient(cfg Config) (*Client, error) {
	baseURL, err := parseBaseURL(cfg.URL)
	if err != nil {
		return nil, err
	}

	if cfg.ProjectID == "" {
		return nil, errors.New("project ID is required")
	}

	if cfg.APIKey == "" {
		return nil, errors.New("API key is required")
	}

	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	}
	transport.TLSClientConfig = cfg.TLSConfig
	transport.ResponseHeaderTimeout = cfg.ResponseHeaderTimeout
	transport.DialContext = (&net.Dialer{
		Timeout: cfg.ConnectTimeout,
	}).DialContext

	httpClient := &http.Client{Transport: transport}

	var fetch tokenFetcher
	switch cfg.AuthMode {
	case AuthModeIAM:
		fetch = iamTokenFetcher(httpClient, cfg.IAMHost, cfg.APIKey)
	case AuthModeCPD:
		if cfg.Username == "" {
			return nil, errors.New("CPD auth mode requires a username")
		}
		fetch = cpdTokenFetcher(httpClient, baseURL, cfg.Username, cfg.APIKey)
	default:
		return nil, fmt.Errorf("unknown auth mode %q", cfg.AuthMode)
	}

	return &Client{
		baseURL:    baseURL,
		projectID:  cfg.ProjectID,
		httpClient: httpClient,
		tokens:     &tokenCache{fetch: fetch, now: time.Now},
		isCPD:      cfg.AuthMode == AuthModeCPD,
	}, nil
}

// Chat requests a chat completion.
func (c *Client) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	resp, err := c.post(ctx, chatPath, req, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read watsonx chat response: %w", err)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("decode watsonx chat response: %w", err)
	}
	chatResp.Raw = string(body)

	return &chatResp, nil
}

// ChatStream requests a streamed chat completion.
// Callers must close the stream; canceling ctx aborts the generation.
func (c *Client) ChatStream(ctx context.Context, req *ChatRequest) (*Stream, error) {
	resp, err := c.post(ctx, chatStreamPath, req, "text/event-stream") //nolint:bodyclose // Closed by Stream.Close.
	if err != nil {
		return nil, err
	}

	return NewStream(resp.Body), nil
}

// post sends a chat request and returns a successful response.
func (c *Client) post(ctx context.Context, path string, req *ChatRequest, accept string) (*http.Response, error) {
	body := *req
	body.ProjectID = c.projectID

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode watsonx request: %w", err)
	}

	endpoint := c.baseURL.JoinPath(path)
	endpoint.RawQuery = url.Values{versionParam: {chatAPIVersion}}.Encode()

	return c.do(ctx, http.MethodPost, endpoint, payload, accept)
}

// do sends the request and returns a successful response.
// A request rejected with 401 is retried once with a new token.
func (c *Client) do(ctx context.Context, method string, endpoint *url.URL, payload []byte, accept string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		token, err := c.tokens.get(ctx)
		if err != nil {
			return nil, err
		}

		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}

		httpReq, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
		if err != nil {
			return nil, fmt.Errorf("create watsonx request: %w", err)
		}
		if payload != nil {
			httpReq.Header.Set("Content-Type", "application/json")
		}
		httpReq.Header.Set("Accept", accept)
		httpReq.Header.Set("Authorization", "Bearer "+token)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("call watsonx: %w", err)
		}

		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			_ = resp.Body.Close()
			c.tokens.invalidate(token)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			defer resp.Body.Close()
			return nil, decodeAPIError(resp)
		}

		return resp, nil
	}
}

// decodeAPIError turns an error response into an APIError.
func decodeAPIError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return fmt.Errorf("read error response with status %s: %w", resp.Status, err)
	}

	var errBody errorBody
	if json.Unmarshal(body, &errBody) == nil && len(errBody.Errors) > 0 {
		return errBody.apiError(resp.StatusCode)
	}

	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}

	return &APIError{StatusCode: resp.StatusCode, Message: message}
}

func parseBaseURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse watsonx URL: %w", err)
	}

	if u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("watsonx URL %q must be an https:// URL", rawURL)
	}

	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("watsonx URL %q must not have a path", rawURL)
	}

	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
}
