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

package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/kubermatic-labs/watsonx-openai-shim/internal/shim"
	"github.com/kubermatic-labs/watsonx-openai-shim/internal/watsonx"
)

// Secrets are read from the environment so they do not show up in the process list.
const (
	defaultMaxTokens               = 1024
	defaultUpstreamConnectTimeout  = 5 * time.Second
	defaultUpstreamResponseTimeout = 50 * time.Second
)

type options struct {
	listenAddr  string
	showVersion bool
	logLevel    slog.Level

	watsonxURL              string
	projectID               string
	username                string
	apiKey                  string
	authMode                string
	iamHost                 string
	caFile                  string
	insecureSkipVerify      bool
	upstreamConnectTimeout  time.Duration
	upstreamResponseTimeout time.Duration

	shim shim.Options
}

func parseOptions(args []string) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet("watsonx-openai-shim", flag.ContinueOnError)
	fs.StringVar(&o.listenAddr, "listen-addr", ":8080", "HTTP listen address")
	fs.BoolVar(&o.showVersion, "version", false, "Print version and exit")
	fs.TextVar(&o.logLevel, "log-level", slog.LevelInfo, "Log level: debug, info, warn or error")
	fs.StringVar(&o.watsonxURL, "watsonx-url", "", "watsonx base URL, e.g. https://eu-de.ml.cloud.ibm.com or the CPD URL (required)")
	fs.StringVar(&o.projectID, "project-id", "", "watsonx.ai project ID sent with every request (required)")
	fs.StringVar(&o.apiKey, "api-key", "", "watsonx.ai API key (required)")
	fs.StringVar(&o.username, "username", "", "CPD username (required when auth-mode=cpd)")
	fs.StringVar(&o.authMode, "auth-mode", string(watsonx.AuthModeCPD),
		"How to obtain tokens: iam (IBM Cloud, needs WXS_API_KEY) or cpd (Cloud Pak for Data, needs WXS_USERNAME and WXS_API_KEY)")
	fs.StringVar(&o.iamHost, "iam-host", "iam.cloud.ibm.com", "IBM Cloud IAM host, used with --auth-mode=iam")
	fs.StringVar(&o.caFile, "ca-file", "", "PEM file with additional CA certificates to trust for watsonx")
	fs.BoolVar(&o.insecureSkipVerify, "insecure-skip-tls-verify", false, "Skip TLS certificate verification for watsonx")
	fs.DurationVar(&o.upstreamConnectTimeout, "upstream-connect-timeout", defaultUpstreamConnectTimeout, "Time to wait for watsonx connection establishment")
	fs.DurationVar(&o.upstreamResponseTimeout, "upstream-response-timeout", defaultUpstreamResponseTimeout, "Time to wait for watsonx response headers")
	fs.IntVar(&o.shim.DefaultMaxTokens, "default-max-tokens", defaultMaxTokens, "Token limit for requests that set none")

	if err := applyEnvVars(fs, "WXS_"); err != nil {
		return nil, err
	}

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	return o, nil
}

// applyEnvVars applies env vars to the given FlagSet.
func applyEnvVars(f *flag.FlagSet, envVarPrefix string) (err error) {
	nameRegex := regexp.MustCompile("[^a-zA-Z0-9]+")
	mapped := map[string]struct{}{}
	f.VisitAll(func(f *flag.Flag) {
		envVarName := envVarPrefix + strings.ToUpper(nameRegex.ReplaceAllString(f.Name, "_"))
		f.Usage = fmt.Sprintf("%s {%s}", f.Usage, envVarName)
		mapped[envVarName] = struct{}{}

		if envVarValue := os.Getenv(envVarName); envVarValue != "" {
			e := f.Value.Set(envVarValue)
			if e != nil && err == nil {
				// Don't expose the provided value with the error message since it could be a secret
				err = fmt.Errorf("invalid environment variable %s value provided: %w", envVarName, e)
			}
		}
	})

	if err != nil {
		f.Usage()
		return err
	}

	// Fail when unsupported env var with the matching prefix is found.
	// (This is to fail fast on incompatible configuration)
	unsupportedEnvVars := []string{}
	for _, entry := range os.Environ() {
		envVarName := strings.SplitN(entry, "=", 2)[0]
		if _, ok := mapped[envVarName]; !ok && strings.HasPrefix(envVarName, envVarPrefix) {
			unsupportedEnvVars = append(unsupportedEnvVars, envVarName)
		}
	}

	if len(unsupportedEnvVars) > 0 {
		f.Usage()
		return fmt.Errorf("unsupported env vars provided: %s", strings.Join(unsupportedEnvVars, ", "))
	}

	return nil
}

func (o *options) watsonxConfig() (watsonx.Config, error) {
	var errs []error
	if o.watsonxURL == "" {
		errs = append(errs, errors.New("--watsonx-url is required"))
	}
	if o.projectID == "" {
		errs = append(errs, errors.New("--project-id is required"))
	}
	if o.apiKey == "" {
		errs = append(errs, errors.New("--api-key is required"))
	}
	if o.insecureSkipVerify && o.authMode == string(watsonx.AuthModeIAM) {
		errs = append(errs, errors.New("--insecure-skip-tls-verify cannot be used with iam auth mode"))
	}

	tlsConfig, err := o.tlsConfig()
	if err != nil {
		errs = append(errs, err)
	}

	if err := errors.Join(errs...); err != nil {
		return watsonx.Config{}, err
	}

	return watsonx.Config{
		URL:                   o.watsonxURL,
		ProjectID:             o.projectID,
		AuthMode:              watsonx.AuthMode(o.authMode),
		IAMHost:               o.iamHost,
		Username:              o.username,
		APIKey:                o.apiKey,
		TLSConfig:             tlsConfig,
		ConnectTimeout:        o.upstreamConnectTimeout,
		ResponseHeaderTimeout: o.upstreamResponseTimeout,
	}, nil
}

func (o *options) tlsConfig() (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: o.insecureSkipVerify, //nolint:gosec // Explicitly requested by the operator.
	}

	if o.caFile == "" {
		return cfg, nil
	}

	pem, err := os.ReadFile(o.caFile) //nolint:gosec // Path is operator-supplied configuration.
	if err != nil {
		return nil, fmt.Errorf("read --ca-file: %w", err)
	}

	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system CA certificates: %w", err)
	}

	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("--ca-file %s contains no PEM certificates", o.caFile)
	}

	cfg.RootCAs = pool

	return cfg, nil
}
