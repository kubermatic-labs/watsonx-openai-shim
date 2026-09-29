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
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOptionsLogLevel(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		env         string
		expected    slog.Level
		expectedErr string
	}{
		{name: "default", expected: slog.LevelInfo},
		{name: "flag", args: []string{"--log-level=debug"}, expected: slog.LevelDebug},
		{name: "env var", env: "warn", expected: slog.LevelWarn},
		{name: "flag overrides env var", args: []string{"--log-level=error"}, env: "debug", expected: slog.LevelError},
		{name: "invalid flag", args: []string{"--log-level=verbose"}, expectedErr: `invalid value "verbose" for flag -log-level`},
		{name: "invalid env var", env: "verbose", expectedErr: "invalid environment variable WXS_LOG_LEVEL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("WXS_LOG_LEVEL", tt.env)
			}

			opts, err := parseOptions(tt.args)
			if tt.expectedErr != "" {
				require.ErrorContains(t, err, tt.expectedErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, opts.logLevel)
		})
	}
}
