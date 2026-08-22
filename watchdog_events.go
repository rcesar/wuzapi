package main

import (
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
)

// watchdogEventPayload turns WhatsApp mutations that would otherwise be
// difficult for webhook consumers to interpret into a stable, narrow schema.
func watchdogEventPayload(rawEvent interface{}, initialSync ...bool) (map[string]interface{}, bool) {
	switch event := rawEvent.(type) {
	case *events.Message:
		protocol := event.Message.GetProtocolMessage()
		if protocol == nil || protocol.GetType() != waE2E.ProtocolMessage_REVOKE || protocol.GetKey().GetID() == "" {
			return nil, false
		}

		actorCategory := "external_participant"
		if event.Info.IsFromMe {
			actorCategory = "company_account"
		}

		return watchdogPayload("MessageDeleted", map[string]interface{}{
			"chatJID":       event.Info.Chat.String(),
			"messageID":     protocol.GetKey().GetID(),
			"timestamp":     event.Info.Timestamp.Format(time.RFC3339Nano),
			"deleteType":    "for_everyone",
			"actorCategory": actorCategory,
		}), true

	case *events.DeleteForMe:
		if event.MessageID == "" || event.ChatJID.IsEmpty() {
			return nil, false
		}
		return watchdogPayload("MessageDeleted", map[string]interface{}{
			"chatJID":       event.ChatJID.String(),
			"messageID":     event.MessageID,
			"timestamp":     event.Timestamp.Format(time.RFC3339Nano),
			"deleteType":    "for_me",
			"actorCategory": "company_account",
			"fromFullSync":  event.FromFullSync,
		}), true

	case *events.Archive:
		if event.Action == nil || event.JID.IsEmpty() {
			return nil, false
		}
		return watchdogPayload("ChatArchive", map[string]interface{}{
			"jid":          event.JID.String(),
			"timestamp":    event.Timestamp.Format(time.RFC3339Nano),
			"archived":     event.Action.GetArchived(),
			"fromFullSync": event.FromFullSync,
		}), true

	case *events.AppState:
		if len(event.Index) < 2 || event.Index[0] != appstate.IndexLock || event.SyncActionValue == nil || event.GetLockChatAction() == nil {
			return nil, false
		}
		return watchdogPayload("ChatLock", map[string]interface{}{
			"jid":          event.Index[1],
			"timestamp":    time.UnixMilli(event.GetTimestamp()).UTC().Format(time.RFC3339Nano),
			"locked":       event.GetLockChatAction().GetLocked(),
			"fromFullSync": len(initialSync) > 0 && initialSync[0],
		}), true
	}

	return nil, false
}

func watchdogPayload(eventType string, event map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"contractVersion": 1,
		"type":            eventType,
		"event":           event,
	}
}
