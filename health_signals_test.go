package main

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestPhoneActivityTime(t *testing.T) {
	const own = "5511999999999"
	const ownLID = "123456789"
	owns := []string{own, ownLID}
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	old := now.Add(-3 * time.Hour)
	third := types.JID{User: "5511888888888", Server: types.DefaultUserServer}
	phone := types.JID{User: own, Server: types.DefaultUserServer}
	lid := types.JID{User: ownLID, Server: types.HiddenUserServer}
	companion := types.JID{User: own, Device: 3, Server: types.DefaultUserServer}

	msg := func(sender types.JID, fromMe bool, ts time.Time) *events.Message {
		return &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Sender: sender, IsFromMe: fromMe}, Timestamp: ts}, Message: &waE2E.Message{}}
	}
	rcpt := func(sender types.JID, ts time.Time) *events.Receipt {
		return &events.Receipt{MessageSource: types.MessageSource{Sender: sender}, Timestamp: ts}
	}
	lockState := &events.AppState{Index: []string{appstate.IndexLock, "1@s.whatsapp.net"}, SyncActionValue: &waSyncAction.SyncActionValue{
		Timestamp:      proto.Int64(old.UnixMilli()),
		LockChatAction: &waSyncAction.LockChatAction{Locked: proto.Bool(true)},
	}}
	tests := []struct {
		name        string
		evt         interface{}
		initialSync bool
		want        bool
		wantTime    time.Time
	}{
		{"own message device 0 uses event time", msg(phone, true, old), false, true, old},
		{"own message zero timestamp falls back to now", msg(phone, true, time.Time{}), false, true, now},
		{"future timestamp clamped", msg(phone, true, now.Add(time.Hour)), false, true, now},
		{"own message from companion", msg(companion, true, now), false, false, time.Time{}},
		{"incoming third-party message", msg(third, false, now), false, false, time.Time{}},
		{"own receipt device 0", rcpt(phone, old), false, true, old},
		{"own LID receipt device 0", rcpt(lid, old), false, true, old},
		{"own receipt companion", rcpt(companion, now), false, false, time.Time{}},
		{"third-party receipt", rcpt(third, now), false, false, time.Time{}},
		{"archive", &events.Archive{Timestamp: old}, false, true, old},
		{"full-sync archive", &events.Archive{Timestamp: old, FromFullSync: true}, false, false, time.Time{}},
		{"full-sync mute", &events.Mute{FromFullSync: true}, false, false, time.Time{}},
		{"mute", &events.Mute{Timestamp: old}, false, true, old},
		{"lock app state", lockState, false, true, old},
		{"app state during initial sync", lockState, true, false, time.Time{}},
		{"connected event", &events.Connected{}, false, false, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := phoneActivityTime(tt.evt, owns, tt.initialSync, now)
			if ok != tt.want || (ok && !got.Equal(tt.wantTime)) {
				t.Fatalf("got (%v,%v) want (%v,%v)", got, ok, tt.wantTime, tt.want)
			}
		})
	}
	if _, ok := phoneActivityTime(rcpt(types.JID{}, now), []string{""}, false, now); ok {
		t.Fatal("empty own user must not match")
	}
}

func TestRecordHealthPhoneActivityMonotonic(t *testing.T) {
	c := &MyClient{health: &sessionHealth{}}
	recent := time.Now().Add(-time.Minute)
	c.recordHealth(&events.Archive{Timestamp: recent})
	if got := c.health.lastPhoneActivityAt.Load(); got != recent.UnixNano() {
		t.Fatalf("got %d want %d", got, recent.UnixNano())
	}
	c.recordHealth(&events.Archive{Timestamp: recent.Add(-time.Hour)}) // offline replay
	if got := c.health.lastPhoneActivityAt.Load(); got != recent.UnixNano() {
		t.Fatalf("moved backwards: %d", got)
	}
}

func TestHealthMap(t *testing.T) {
	keys := []string{"lastEventAt", "lastPhoneActivityAt", "lastConnectedAt", "lastKeepAliveTimeoutAt", "lastDisconnectAt", "lastDisconnectReason"}

	t.Run("unknown user is all null", func(t *testing.T) {
		m := healthMap("health-unknown")
		for _, k := range keys {
			if v, ok := m[k]; !ok || v != nil {
				t.Fatalf("%s = %v, want nil", k, v)
			}
		}
		if m["sessionState"] != "stopped" {
			t.Fatalf("sessionState = %v", m["sessionState"])
		}
	})

	t.Run("set values are RFC3339", func(t *testing.T) {
		const id = "health-set"
		defer deleteHealth(id)
		h := healthFor(id)
		ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		h.lastEventAt.Store(ts.UnixNano())
		h.lastPhoneActivityAt.Store(ts.UnixNano())
		h.lastKeepAliveTimeoutAt.Store(ts.UnixNano())
		h.lastConnectedAt.Store(ts.UnixNano())
		h.recordDisconnect("stream_error")
		m := healthMap(id)
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

func TestHealthSurvivesClientReplacement(t *testing.T) {
	const id = "health-survive"
	defer deleteHealth(id)
	old := &MyClient{userID: id, health: healthFor(id)}
	old.recordHealth(&events.Connected{})
	old.scheduleSessionReplacement("keepalive_timeout")

	// the attempt is cleaned up; a new one reuses the same store
	_ = old
	m := healthMap(id)
	if m["lastEventAt"] == nil || m["lastConnectedAt"] == nil || m["lastDisconnectReason"] != "keepalive_timeout" {
		t.Fatalf("health lost: %v", m)
	}
	if healthFor(id) != old.health {
		t.Fatal("store must return the same health for the user")
	}
}

func TestSessionState(t *testing.T) {
	const id = "state-user"
	if got := sessionState(id); got != "stopped" {
		t.Fatalf("got %s", got)
	}
	session := newSessionControl(id, 1)
	sessionRegistry.Lock()
	sessionRegistry.current[id] = session
	sessionRegistry.Unlock()
	defer func() {
		sessionRegistry.Lock()
		delete(sessionRegistry.current, id)
		sessionRegistry.Unlock()
		clientManager.DeleteWhatsmeowClient(id)
	}()
	if got := sessionState(id); got != "restarting" {
		t.Fatalf("got %s", got)
	}
	clientManager.SetWhatsmeowClient(id, &whatsmeow.Client{})
	if got := sessionState(id); got != "running" {
		t.Fatalf("got %s", got)
	}
}

func TestRecordHealth(t *testing.T) {
	c := &MyClient{health: &sessionHealth{}}
	c.recordHealth(&events.Connected{})
	if c.health.lastEventAt.Load() == 0 || c.health.lastConnectedAt.Load() == 0 || c.health.lastPhoneActivityAt.Load() != 0 {
		t.Fatal("any event sets lastEventAt; Connected sets lastConnectedAt only")
	}
	c.recordHealth(&events.KeepAliveTimeout{})
	if c.health.lastKeepAliveTimeoutAt.Load() == 0 {
		t.Fatal("keepalive timeout not recorded")
	}
	c.recordHealth(&events.StreamReplaced{})
	if at, r := c.health.disconnect(); r != "stream_replaced" || at == 0 {
		t.Fatal("disconnect not recorded")
	}
}

func TestPushNameSettingDoesNotMoveConnected(t *testing.T) {
	c := &MyClient{health: &sessionHealth{}}
	c.recordHealth(&events.PushNameSetting{})
	if c.health.lastConnectedAt.Load() != 0 {
		t.Fatal("PushNameSetting must not set lastConnectedAt")
	}
}

func TestLogoutIsTerminalUntilConnected(t *testing.T) {
	c := &MyClient{health: &sessionHealth{}}
	c.recordHealth(&events.LoggedOut{})
	at, _ := c.health.disconnect()
	c.scheduleSessionReplacement("keepalive_timeout")
	c.recordHealth(&events.Disconnected{})
	if at2, r := c.health.disconnect(); r != "logged_out" || at2 != at {
		t.Fatalf("logout overwritten: %s", r)
	}
	c.recordHealth(&events.Connected{})
	if _, r := c.health.disconnect(); r != "" {
		t.Fatalf("logout not cleared on Connected: %s", r)
	}
	c.scheduleSessionReplacement("keepalive_timeout")
	if _, r := c.health.disconnect(); r != "keepalive_timeout" {
		t.Fatal("reason should record after logout cleared")
	}
}

func TestScheduleSessionReplacementRecordsReason(t *testing.T) {
	c := &MyClient{health: &sessionHealth{}}
	c.scheduleSessionReplacement("keepalive_timeout")
	if at, r := c.health.disconnect(); r != "keepalive_timeout" || at == 0 {
		t.Fatal("reason not recorded")
	}
}

func TestNextReplacementCount(t *testing.T) {
	tests := []struct {
		name   string
		count  int
		uptime time.Duration
		want   int
	}{
		{"rapid loop keeps counting", 3, 30 * time.Second, 4},
		{"just under threshold", 4, streamReplacementResetAfter - time.Second, 5},
		{"long-lived attempt resets", 5, streamReplacementResetAfter, 1},
		{"first restart", 0, time.Second, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextReplacementCount(tt.count, tt.uptime); got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}
