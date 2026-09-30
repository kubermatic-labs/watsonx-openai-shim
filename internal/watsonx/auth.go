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

package watsonx

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	iamTokenPath     = "/identity/token"
	cpdAuthorizePath = "/icp4d-api/v1/authorize"

	// tokenRefreshMargin is how long before its expiry a token is replaced.
	tokenRefreshMargin = 5 * time.Minute

	// fallbackTokenLifetime applies when a token response carries no readable expiry.
	fallbackTokenLifetime = time.Hour
)

// tokenFetcher obtains a new bearer token and its expiry.
type tokenFetcher func(ctx context.Context) (token string, expiry time.Time, err error)

// tokenCache shares one token between concurrent requests and replaces it before it expires.
type tokenCache struct {
	fetch tokenFetcher
	now   func() time.Time

	mu        sync.Mutex
	token     string
	refreshAt time.Time
}

func (c *tokenCache) get(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && c.now().Before(c.refreshAt) {
		return c.token, nil
	}

	token, expiry, err := c.fetch(ctx)
	if err != nil {
		return "", fmt.Errorf("obtain watsonx token: %w", err)
	}

	lifetime := expiry.Sub(c.now())
	c.token = token
	c.refreshAt = expiry.Add(-min(tokenRefreshMargin, max(lifetime/2, 0)))

	return token, nil
}

// invalidate drops the token, unless another request replaced it already.
func (c *tokenCache) invalidate(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token == token {
		c.token = ""
	}
}

// iamTokenFetcher exchanges an API key at IBM Cloud IAM.
func iamTokenFetcher(httpClient *http.Client, iamHost, apiKey string) tokenFetcher {
	tokenURL := (&url.URL{Scheme: "https", Host: iamHost, Path: iamTokenPath}).String()

	return func(ctx context.Context) (string, time.Time, error) {
		form := url.Values{
			"grant_type": {"urn:ibm:params:oauth:grant-type:apikey"},
			"apikey":     {apiKey},
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
		if err != nil {
			return "", time.Time{}, fmt.Errorf("create IAM token request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		var resp struct {
			AccessToken string `json:"access_token"`
			Expiration  int64  `json:"expiration"`
		}
		if err := doTokenRequest(httpClient, req, &resp); err != nil {
			return "", time.Time{}, fmt.Errorf("IAM token request: %w", err)
		}

		if resp.AccessToken == "" {
			return "", time.Time{}, errors.New("IAM token response contains no token")
		}

		expiry := time.Now().Add(fallbackTokenLifetime)
		if resp.Expiration != 0 {
			expiry = time.Unix(resp.Expiration, 0)
		}

		return resp.AccessToken, expiry, nil
	}
}

// cpdTokenFetcher exchanges a username and API key at a Cloud Pak for Data instance.
func cpdTokenFetcher(httpClient *http.Client, baseURL *url.URL, username, apiKey string) tokenFetcher {
	authorizeURL := baseURL.JoinPath(cpdAuthorizePath).String()

	return func(ctx context.Context) (string, time.Time, error) {
		payload, err := json.Marshal(map[string]string{"username": username, "api_key": apiKey})
		if err != nil {
			return "", time.Time{}, fmt.Errorf("encode CPD authorize request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, authorizeURL, bytes.NewReader(payload))
		if err != nil {
			return "", time.Time{}, fmt.Errorf("create CPD authorize request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		var resp struct {
			Token string `json:"token"`
		}
		if err := doTokenRequest(httpClient, req, &resp); err != nil {
			return "", time.Time{}, fmt.Errorf("CPD authorize: %w", err)
		}

		if resp.Token == "" {
			return "", time.Time{}, errors.New("CPD authorize response contains no token")
		}

		return resp.Token, tokenExpiry(resp.Token), nil
	}
}

func doTokenRequest(httpClient *http.Client, req *http.Request, out any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return decodeAPIError(resp)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

// tokenExpiry reads the exp claim of a JWT without verifying it.
func tokenExpiry(token string) time.Time {
	fallback := time.Now().Add(fallbackTokenLifetime)

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fallback
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fallback
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil || claims.Exp == 0 {
		return fallback
	}

	return time.Unix(claims.Exp, 0)
}
