package main

import (
	"sync"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

const logoutReason = "logged_out"

// sessionHealth keeps in-memory liveness timestamps (UnixNano, 0 = never) for
// one user. It outlives individual MyClient attempts so the signals stay
// readable while a session is restarting. They are polled through
// GET /admin/users instead of being pushed over webhooks.
type sessionHealth struct {
	lastEventAt            atomic.Int64
	lastPhoneActivityAt    atomic.Int64
	lastConnectedAt        atomic.Int64
	lastKeepAliveTimeoutAt atomic.Int64

	mu                   sync.Mutex
	lastDisconnectAt     int64
	lastDisconnectReason string
}

// recordDisconnect stores time and reason together. A logout is terminal until
// the next Connected, so later reasons never overwrite it.
func (h *sessionHealth) recordDisconnect(reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastDisconnectReason == logoutReason && reason != logoutReason {
		return
	}
	h.lastDisconnectReason = reason
	h.lastDisconnectAt = time.Now().UnixNano()
}

func (h *sessionHealth) markConnected(now int64) {
	h.lastConnectedAt.Store(now)
	h.mu.Lock()
	if h.lastDisconnectReason == logoutReason {
		h.lastDisconnectReason = ""
		h.lastDisconnectAt = 0
	}
	h.mu.Unlock()
}

func (h *sessionHealth) disconnect() (int64, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastDisconnectAt, h.lastDisconnectReason
}

// healthRegistry holds one sessionHealth per user id for the process lifetime
// (until the user is deleted).
var healthRegistry = struct {
	sync.Mutex
	byUser map[string]*sessionHealth
}{byUser: make(map[string]*sessionHealth)}

func healthFor(userID string) *sessionHealth {
	healthRegistry.Lock()
	defer healthRegistry.Unlock()
	h := healthRegistry.byUser[userID]
	if h == nil {
		h = &sessionHealth{}
		healthRegistry.byUser[userID] = h
	}
	return h
}

func lookupHealth(userID string) *sessionHealth {
	healthRegistry.Lock()
	defer healthRegistry.Unlock()
	return healthRegistry.byUser[userID]
}

func deleteHealth(userID string) {
	healthRegistry.Lock()
	delete(healthRegistry.byUser, userID)
	healthRegistry.Unlock()
}

// sessionState tells a poller whether a missing connection is expected:
// "running" (client loaded), "restarting" (session registered, no client:
// backoff/QR/retry) or "stopped" (no session).
func sessionState(userID string) string {
	if clientManager.GetWhatsmeowClient(userID) != nil {
		return "running"
	}
	if currentSession(userID) != nil {
		return "restarting"
	}
	return "stopped"
}

// phoneActivityTime reports whether evt was produced by the account's primary
// device (the phone), as opposed to a companion device or a third party, and
// when it happened according to the event itself. Full-sync and initial-sync
// replays are not live activity. A zero event timestamp falls back to now.
func phoneActivityTime(evt interface{}, ownUsers []string, initialSync bool, now time.Time) (time.Time, bool) {
	isOwn := func(j types.JID) bool {
		if j.Device != 0 || j.User == "" {
			return false
		}
		for _, u := range ownUsers {
			if u != "" && u == j.User {
				return true
			}
		}
		return false
	}
	var ts time.Time
	switch e := evt.(type) {
	case *events.Message:
		if !e.Info.IsFromMe || e.Info.Sender.Device != 0 {
			return time.Time{}, false
		}
		ts = e.Info.Timestamp
	case *events.Receipt:
		if !isOwn(e.Sender) {
			return time.Time{}, false
		}
		ts = e.Timestamp
	case *events.DeleteForMe:
		if e.FromFullSync {
			return time.Time{}, false
		}
		ts = e.Timestamp
	case *events.Archive:
		if e.FromFullSync {
			return time.Time{}, false
		}
		ts = e.Timestamp
	case *events.ClearChat:
		if e.FromFullSync {
			return time.Time{}, false
		}
		ts = e.Timestamp
	case *events.DeleteChat:
		if e.FromFullSync {
			return time.Time{}, false
		}
		ts = e.Timestamp
	case *events.Mute:
		if e.FromFullSync {
			return time.Time{}, false
		}
		ts = e.Timestamp
	case *events.Pin:
		if e.FromFullSync {
			return time.Time{}, false
		}
		ts = e.Timestamp
	case *events.AppState:
		if initialSync {
			return time.Time{}, false
		}
		if _, ok := watchdogEventPayload(e, nil); !ok {
			return time.Time{}, false
		}
		if ms := e.GetTimestamp(); ms > 0 {
			ts = time.UnixMilli(ms)
		}
	default:
		return time.Time{}, false
	}
	if ts.IsZero() || ts.After(now) {
		return now, true
	}
	return ts, true
}

// storeMax advances v to val, never moving it backwards.
func storeMax(v *atomic.Int64, val int64) {
	for {
		cur := v.Load()
		if val <= cur || v.CompareAndSwap(cur, val) {
			return
		}
	}
}

// disconnectReason maps a connection-loss event to a short reason, or "" when
// the event is not one.
func disconnectReason(evt interface{}) string {
	switch evt.(type) {
	case *events.Disconnected:
		return "disconnected"
	case *events.ConnectFailure:
		return "connect_failure"
	case *events.LoggedOut:
		return "logged_out"
	case *events.StreamReplaced:
		return "stream_replaced"
	case *events.TemporaryBan:
		return "temporary_ban"
	case *events.ClientOutdated:
		return "client_outdated"
	case *events.StreamError:
		return "stream_error"
	}
	return ""
}

// recordHealth updates the health timestamps for an incoming event. It must run
// before any subscription filtering.
func (mycli *MyClient) recordHealth(evt interface{}) {
	h := mycli.health
	if h == nil {
		return
	}
	now := time.Now().UnixNano()
	h.lastEventAt.Store(now)

	var ownUsers []string
	if mycli.WAClient != nil && mycli.WAClient.Store != nil {
		if id := mycli.WAClient.Store.ID; id != nil {
			ownUsers = append(ownUsers, id.User)
		}
		ownUsers = append(ownUsers, mycli.WAClient.Store.LID.User)
	}
	if ts, ok := phoneActivityTime(evt, ownUsers, mycli.regularLowSync.Load(), time.Now()); ok {
		storeMax(&h.lastPhoneActivityAt, ts.UnixNano())
	}
	switch evt.(type) {
	case *events.Connected:
		h.markConnected(now)
	case *events.KeepAliveTimeout:
		h.lastKeepAliveTimeoutAt.Store(now)
	}
	if reason := disconnectReason(evt); reason != "" {
		h.recordDisconnect(reason)
	}
}

func healthTime(unixNano int64) interface{} {
	if unixNano == 0 {
		return nil
	}
	return time.Unix(0, unixNano).UTC().Format(time.RFC3339)
}

// healthMap builds the "health" block of /admin/users. A user without any
// recorded signal yields all-null values.
func healthMap(userID string) map[string]interface{} {
	out := map[string]interface{}{
		"lastEventAt":            nil,
		"lastPhoneActivityAt":    nil,
		"lastConnectedAt":        nil,
		"lastKeepAliveTimeoutAt": nil,
		"lastDisconnectAt":       nil,
		"lastDisconnectReason":   nil,
		"sessionState":           sessionState(userID),
	}
	h := lookupHealth(userID)
	if h == nil {
		return out
	}
	out["lastEventAt"] = healthTime(h.lastEventAt.Load())
	out["lastPhoneActivityAt"] = healthTime(h.lastPhoneActivityAt.Load())
	out["lastConnectedAt"] = healthTime(h.lastConnectedAt.Load())
	out["lastKeepAliveTimeoutAt"] = healthTime(h.lastKeepAliveTimeoutAt.Load())
	at, reason := h.disconnect()
	out["lastDisconnectAt"] = healthTime(at)
	if reason != "" {
		out["lastDisconnectReason"] = reason
	}
	return out
}
