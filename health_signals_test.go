package main

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestIsPhoneOriginated(t *testing.T) {
	const own = "5511999999999"
	third := types.JID{User: "5511888888888", Server: types.DefaultUserServer}
	phone := types.JID{User: own, Server: types.DefaultUserServer}
	companion := types.JID{User: own, Device: 3, Server: types.DefaultUserServer}

	msg := func(sender types.JID, fromMe bool) *events.Message {
		return &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Sender: sender, IsFromMe: fromMe}}, Message: &waE2E.Message{}}
	}
	tests := []struct {
		name string
		evt  interface{}
		want bool
	}{
		{"own message device 0", msg(phone, true), true},
		{"own message from companion", msg(companion, true), false},
		{"incoming third-party message", msg(third, false), false},
		{"own receipt device 0", &events.Receipt{MessageSource: types.MessageSource{Sender: phone}}, true},
		{"own receipt companion", &events.Receipt{MessageSource: types.MessageSource{Sender: companion}}, false},
		{"third-party receipt", &events.Receipt{MessageSource: types.MessageSource{Sender: third}}, false},
		{"archive app state", &events.Archive{}, true},
		{"mute app state", &events.Mute{}, true},
		{"connected event", &events.Connected{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPhoneOriginated(tt.evt, own); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
	if isPhoneOriginated(&events.Receipt{MessageSource: types.MessageSource{Sender: types.JID{}}}, "") {
		t.Fatal("empty own user must not match")
	}
}

func TestHealthMap(t *testing.T) {
	keys := []string{"lastEventAt", "lastPhoneActivityAt", "lastConnectedAt", "lastKeepAliveTimeoutAt", "lastDisconnectAt", "lastDisconnectReason"}

	for name, c := range map[string]*MyClient{"nil client": nil, "zero values": {}} {
		t.Run(name, func(t *testing.T) {
			m := healthMap(c)
			if len(m) != len(keys) {
				t.Fatalf("got %d keys", len(m))
			}
			for _, k := range keys {
				if v, ok := m[k]; !ok || v != nil {
					t.Fatalf("%s = %v, want nil", k, v)
				}
			}
		})
	}

	t.Run("set values", func(t *testing.T) {
		c := &MyClient{}
		ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		c.health.lastEventAt.Store(ts.UnixNano())
		c.health.lastPhoneActivityAt.Store(ts.UnixNano())
		c.health.lastKeepAliveTimeoutAt.Store(ts.UnixNano())
		c.lastConnected.Store(ts.UnixNano())
		c.health.recordDisconnect("stream_error")
		m := healthMap(c)
		for _, k := range []string{"lastEventAt", "lastPhoneActivityAt", "lastConnectedAt", "lastKeepAliveTimeoutAt"} {
			if m[k] != "2026-01-02T03:04:05Z" {
				t.Fatalf("%s = %v", k, m[k])
			}
		}
		if _, err := time.Parse(time.RFC3339, m["lastDisconnectAt"].(string)); err != nil {
			t.Fatal(err)
		}
		if m["lastDisconnectReason"] != "stream_error" {
			t.Fatalf("reason = %v", m["lastDisconnectReason"])
		}
	})
}

func TestRecordHealth(t *testing.T) {
	c := &MyClient{}
	c.recordHealth(&events.Connected{})
	if c.health.lastEventAt.Load() == 0 || c.health.lastPhoneActivityAt.Load() != 0 {
		t.Fatal("any event sets lastEventAt only")
	}
	c.recordHealth(&events.KeepAliveTimeout{})
	if c.health.lastKeepAliveTimeoutAt.Load() == 0 {
		t.Fatal("keepalive timeout not recorded")
	}
	c.recordHealth(&events.StreamReplaced{})
	if c.health.lastDisconnectReason.Load() != "stream_replaced" || c.health.lastDisconnectAt.Load() == 0 {
		t.Fatal("disconnect not recorded")
	}
}

func TestScheduleSessionReplacementRecordsReason(t *testing.T) {
	c := &MyClient{}
	c.scheduleSessionReplacement("keepalive_timeout")
	if c.health.lastDisconnectReason.Load() != "keepalive_timeout" || c.health.lastDisconnectAt.Load() == 0 {
		t.Fatal("reason not recorded")
	}
}
