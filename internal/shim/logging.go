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

package shim

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
)

const (
	// requestIDHeader returns the request ID to the client, so that it can be found in the logs.
	requestIDHeader = "X-Request-Id"

	// requestIDBytes is the number of random bytes in a request ID.
	requestIDBytes = 8
)

type loggerKey struct{}

// withRequestLogger assigns an ID to every request and adds a logger carrying it to the context.
func withRequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set(requestIDHeader, id)

		logger := slog.Default().With("requestID", id)
		logger.Debug("Received request", "method", r.Method, "path", r.URL.Path)

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), loggerKey{}, logger)))
	})
}

// loggerFrom returns the logger of the request, or the default logger outside a request.
func loggerFrom(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}

	return slog.Default()
}

func newRequestID() string {
	b := make([]byte, requestIDBytes)
	_, _ = rand.Read(b) // never fails, see crypto/rand.Read

	return hex.EncodeToString(b)
}
