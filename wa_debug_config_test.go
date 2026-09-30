package main

import "testing"

func TestResolveWADebug(t *testing.T) {
	tests := []struct {
		name         string
		flagValue    string
		envValue     string
		flagExplicit bool
		want         string
		wantErr      bool
	}{
		{name: "default off"},
		{name: "empty environment off", envValue: "", want: ""},
		{name: "info from environment", envValue: "INFO", want: "INFO"},
		{name: "debug from environment", envValue: "DEBUG", want: "DEBUG"},
		{name: "normalized environment", envValue: " debug ", want: "DEBUG"},
		{name: "explicit flag wins", flagValue: "INFO", envValue: "DEBUG", flagExplicit: true, want: "INFO"},
		{name: "explicit empty flag disables", envValue: "DEBUG", flagExplicit: true, want: ""},
		{name: "invalid environment", envValue: "TRACE", wantErr: true},
		{name: "invalid explicit flag", flagValue: "TRACE", flagExplicit: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveWADebug(tt.flagValue, tt.envValue, tt.flagExplicit)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveWADebug() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("resolveWADebug() = %q, want %q", got, tt.want)
			}
		})
	}
}
