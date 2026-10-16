package main

import "testing"

type fakeChecker struct{ connected bool }

func (f fakeChecker) IsConnected() bool { return f.connected }

func TestAlreadyConnected(t *testing.T) {
	tests := []struct {
		name string
		c    connectionChecker
		want bool
	}{
		{"no client loaded", nil, false},
		{"loaded but disconnected", fakeChecker{false}, false},
		{"loaded and connected", fakeChecker{true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := alreadyConnected(tt.c); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestLoadedConnectionCheckerNilWhenNoClient(t *testing.T) {
	if c := loadedConnectionChecker("connect-guard-no-client"); c != nil {
		t.Fatal("expected a true nil interface when no client is loaded")
	}
}
