package main

import (
	"sync/atomic"
	"time"

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

// isPhoneOriginated reports whether evt was produced by the account's primary
// device (the phone), as opposed to a companion device or a third party.
func isPhoneOriginated(evt interface{}, ownUser string) bool {
	switch e := evt.(type) {
	case *events.Message:
		return e.Info.IsFromMe && e.Info.Sender.Device == 0
	case *events.Receipt:
		return ownUser != "" && e.Sender.User == ownUser && e.Sender.Device == 0
	case *events.DeleteForMe, *events.Archive, *events.ClearChat, *events.DeleteChat,
		*events.Mute, *events.Pin:
		return true
	case *events.AppState:
		_, ok := watchdogEventPayload(e, nil)
		return ok
	}
	return false
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

	ownUser := ""
	if mycli.WAClient != nil && mycli.WAClient.Store != nil && mycli.WAClient.Store.ID != nil {
		ownUser = mycli.WAClient.Store.ID.User
	}
	if isPhoneOriginated(evt, ownUser) {
		mycli.health.lastPhoneActivityAt.Store(now)
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
