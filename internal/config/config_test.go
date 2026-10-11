package config

import (
	"maps"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad(t *testing.T) {
	t.Parallel()

	base := map[string]string{
		"API_KEY":      "secret",
		"DATABASE_URL": "postgres://localhost/db",
	}
	with := func(kv ...string) map[string]string {
		m := maps.Clone(base)
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr []string
	}{
		{
			name: "defaults",
			env:  base,
			want: Config{
				APIKey:          "secret",
				ListenAddr:      DefaultListenAddr,
				DatabaseURL:     "postgres://localhost/db",
				SlotGranularity: DefaultSlotGranularity,
			},
		},
		{
			name: "all set",
			env: with(
				"LISTEN_ADDR", "127.0.0.1:9000",
				"SLOT_GRANULARITY", "30m",
				"OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317",
			),
			want: Config{
				APIKey:          "secret",
				ListenAddr:      "127.0.0.1:9000",
				DatabaseURL:     "postgres://localhost/db",
				SlotGranularity: 30 * time.Minute,
				OTLPEndpoint:    "localhost:4317",
			},
		},
		{
			name:    "missing required",
			env:     map[string]string{},
			wantErr: []string{"API_KEY is required", "DATABASE_URL is required"},
		},
		{
			name:    "unparseable granularity",
			env:     with("SLOT_GRANULARITY", "soon"),
			wantErr: []string{"SLOT_GRANULARITY"},
		},
		{
			name:    "sub-minute granularity",
			env:     with("SLOT_GRANULARITY", "90s"),
			wantErr: []string{"whole number of minutes"},
		},
		{
			name:    "negative granularity",
			env:     with("SLOT_GRANULARITY", "-15m"),
			wantErr: []string{"whole number of minutes"},
		},
		{
			name:    "granularity does not divide a day",
			env:     with("SLOT_GRANULARITY", "7m"),
			wantErr: []string{"divide a day"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Load(env(tt.env))
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatalf("Load() error = nil, want errors containing %q", tt.wantErr)
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("Load() error = %q, want it to contain %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
