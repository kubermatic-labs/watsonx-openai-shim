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

// Command watsonx-openai-shim serves watsonx text generation as the OpenAI chat completion API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kubermatic-labs/watsonx-openai-shim/internal/shim"
	"github.com/kubermatic-labs/watsonx-openai-shim/internal/version"
	"github.com/kubermatic-labs/watsonx-openai-shim/internal/watsonx"
)

const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 30 * time.Second
)

func main() {
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: opts.logLevel})))

	appVersion := version.NewAppVersion()
	if opts.showVersion {
		fmt.Printf("watsonx-openai-shim %s (commit: %s)\n", appVersion.GitVersion, appVersion.GitHead)
		return
	}

	if err := run(opts, appVersion); err != nil {
		slog.Error("watsonx-openai-shim failed", "error", err)
		os.Exit(1)
	}
}

func run(opts *options, appVersion version.AppVersion) error {
	watsonxConfig, err := opts.watsonxConfig()
	if err != nil {
		return err
	}

	client, err := watsonx.NewClient(watsonxConfig)
	if err != nil {
		return fmt.Errorf("create watsonx client: %w", err)
	}

	handler, err := shim.NewHandler(client, opts.shim)
	if err != nil {
		return fmt.Errorf("create handler: %w", err)
	}

	// No write timeout: streamed completions last as long as the generation does.
	srv := &http.Server{
		Addr:              opts.listenAddr,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("watsonx-openai-shim listening",
			"version", appVersion.GitVersion,
			"addr", opts.listenAddr,
			"watsonxURL", watsonxConfig.URL,
			"authMode", watsonxConfig.AuthMode,
		)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}

	return nil
}
