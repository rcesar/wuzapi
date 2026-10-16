package main

import (
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// sessionHealth keeps in-memory liveness timestamps (UnixNano, 0 = never) for
// one session. They are polled through GET /admin/users instead of being pushed
// over webhooks.
type sessionHealth struct {
	lastEventAt            atomic.Int64
	lastPhoneActivityAt    atomic.Int64
	lastKeepAliveTimeoutAt atomic.Int64
	lastDisconnectAt       atomic.Int64
	lastDisconnectReason   atomic.Value // string
}

func (h *sessionHealth) recordDisconnect(reason string) {
	h.lastDisconnectReason.Store(reason)
	h.lastDisconnectAt.Store(time.Now().UnixNano())
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
	now := time.Now().UnixNano()
	mycli.health.lastEventAt.Store(now)

	var ownUsers []string
	if mycli.WAClient != nil && mycli.WAClient.Store != nil {
		if id := mycli.WAClient.Store.ID; id != nil {
			ownUsers = append(ownUsers, id.User)
		}
		ownUsers = append(ownUsers, mycli.WAClient.Store.LID.User)
	}
	if ts, ok := phoneActivityTime(evt, ownUsers, mycli.regularLowSync.Load(), time.Now()); ok {
		storeMax(&mycli.health.lastPhoneActivityAt, ts.UnixNano())
	}
	if _, ok := evt.(*events.KeepAliveTimeout); ok {
		mycli.health.lastKeepAliveTimeoutAt.Store(now)
	}
	if reason := disconnectReason(evt); reason != "" {
		mycli.health.recordDisconnect(reason)
	}
}

func healthTime(unixNano int64) interface{} {
	if unixNano == 0 {
		return nil
	}
	return time.Unix(0, unixNano).UTC().Format(time.RFC3339)
}

// healthMap builds the "health" block of /admin/users. A nil client (session
// not loaded) yields all-null values.
func healthMap(mycli *MyClient) map[string]interface{} {
	out := map[string]interface{}{
		"lastEventAt":            nil,
		"lastPhoneActivityAt":    nil,
		"lastConnectedAt":        nil,
		"lastKeepAliveTimeoutAt": nil,
		"lastDisconnectAt":       nil,
		"lastDisconnectReason":   nil,
	}
	if mycli == nil {
		return out
	}
	out["lastEventAt"] = healthTime(mycli.health.lastEventAt.Load())
	out["lastPhoneActivityAt"] = healthTime(mycli.health.lastPhoneActivityAt.Load())
	out["lastConnectedAt"] = healthTime(mycli.lastConnected.Load())
	out["lastKeepAliveTimeoutAt"] = healthTime(mycli.health.lastKeepAliveTimeoutAt.Load())
	out["lastDisconnectAt"] = healthTime(mycli.health.lastDisconnectAt.Load())
	if r, ok := mycli.health.lastDisconnectReason.Load().(string); ok && r != "" {
		out["lastDisconnectReason"] = r
	}
	return out
}
