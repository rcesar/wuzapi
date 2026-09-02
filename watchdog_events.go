package main

import (
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// watchdogEventPayload turns WhatsApp mutations that would otherwise be
// difficult for webhook consumers to interpret into a stable, narrow schema.
func watchdogEventPayload(rawEvent interface{}, lookup MessageLookup, initialSync ...bool) (map[string]interface{}, bool) {
	switch event := rawEvent.(type) {
	case *events.Message:
		protocol := event.Message.GetProtocolMessage()
		if protocol == nil || protocol.GetType() != waE2E.ProtocolMessage_REVOKE || protocol.GetKey().GetID() == "" {
			return nil, false
		}

		targetMessageID := protocol.GetKey().GetID()
		chatJID := event.Info.Chat.String()

		actorCategory := "external_participant"
		if event.Info.IsFromMe {
			actorCategory = "company_account"
		}

		actorPhoneNumber := event.Info.Sender.User
		if event.Info.IsFromMe && lookup != nil && actorPhoneNumber == "" {
			actorPhoneNumber = lookup.MyPhoneNumber()
		}

		pushName := event.Info.PushName
		if pushName == "" && lookup != nil && !event.Info.Sender.IsEmpty() {
			pushName = lookup.LookupContact(event.Info.Sender.String())
		}

		senderPhoneNumber := ""
		messageContent := ""

		if lookup != nil {
			origSenderJID, origText, found := lookup.LookupMessage(chatJID, targetMessageID)
			if found {
				messageContent = origText
				if origSenderJID == "me" {
					senderPhoneNumber = lookup.MyPhoneNumber()
				} else if origSenderJID != "" {
					senderPhoneNumber = extractPhoneNumber(origSenderJID)
					if pushName == "" || origSenderJID != event.Info.Sender.String() {
						if origPush := lookup.LookupContact(origSenderJID); origPush != "" {
							pushName = origPush
						}
					}
				}
			}
		}

		if senderPhoneNumber == "" {
			senderPhoneNumber = actorPhoneNumber
		}

		phoneNumber := senderPhoneNumber
		if phoneNumber == "" {
			phoneNumber = actorPhoneNumber
		}

		return watchdogPayload("MessageDeleted", map[string]interface{}{
			"chatJID":           chatJID,
			"messageID":         targetMessageID,
			"timestamp":         event.Info.Timestamp.Format(time.RFC3339Nano),
			"deleteType":        "for_everyone",
			"actorCategory":     actorCategory,
			"phoneNumber":       phoneNumber,
			"senderPhoneNumber": senderPhoneNumber,
			"actorPhoneNumber":  actorPhoneNumber,
			"pushName":          pushName,
			"messageContent":    messageContent,
		}), true

	case *events.DeleteForMe:
		if event.MessageID == "" || event.ChatJID.IsEmpty() {
			return nil, false
		}

		chatJID := event.ChatJID.String()
		actorCategory := "company_account"
		actorPhoneNumber := ""
		if lookup != nil {
			actorPhoneNumber = lookup.MyPhoneNumber()
		}

		senderPhoneNumber := ""
		pushName := ""
		messageContent := ""

		if lookup != nil {
			origSenderJID, origText, found := lookup.LookupMessage(chatJID, event.MessageID)
			if found {
				messageContent = origText
				if origSenderJID == "me" {
					senderPhoneNumber = lookup.MyPhoneNumber()
				} else if origSenderJID != "" {
					senderPhoneNumber = extractPhoneNumber(origSenderJID)
					pushName = lookup.LookupContact(origSenderJID)
				}
			}
		}

		if senderPhoneNumber == "" && !event.SenderJID.IsEmpty() {
			senderPhoneNumber = event.SenderJID.User
			if pushName == "" && lookup != nil {
				pushName = lookup.LookupContact(event.SenderJID.String())
			}
		}

		if senderPhoneNumber == "" && event.IsFromMe && lookup != nil {
			senderPhoneNumber = lookup.MyPhoneNumber()
		}

		phoneNumber := senderPhoneNumber
		if phoneNumber == "" {
			phoneNumber = actorPhoneNumber
		}

		return watchdogPayload("MessageDeleted", map[string]interface{}{
			"chatJID":           chatJID,
			"messageID":         event.MessageID,
			"timestamp":         event.Timestamp.Format(time.RFC3339Nano),
			"deleteType":        "for_me",
			"actorCategory":     actorCategory,
			"fromFullSync":      event.FromFullSync,
			"phoneNumber":       phoneNumber,
			"senderPhoneNumber": senderPhoneNumber,
			"actorPhoneNumber":  actorPhoneNumber,
			"pushName":          pushName,
			"messageContent":    messageContent,
		}), true

	case *events.Archive:
		if event.Action == nil || event.JID.IsEmpty() {
			return nil, false
		}

		chatJID := event.JID.String()
		phoneNumber := ""
		if event.JID.Server == types.DefaultUserServer || event.JID.Server == types.LegacyUserServer {
			phoneNumber = event.JID.User
		}
		pushName := ""
		if lookup != nil {
			pushName = lookup.LookupContact(chatJID)
		}

		return watchdogPayload("ChatArchive", map[string]interface{}{
			"jid":          chatJID,
			"timestamp":    event.Timestamp.Format(time.RFC3339Nano),
			"archived":     event.Action.GetArchived(),
			"fromFullSync": event.FromFullSync,
			"phoneNumber":  phoneNumber,
			"pushName":     pushName,
		}), true

	case *events.AppState:
		if len(event.Index) < 2 || event.Index[0] != appstate.IndexLock || event.SyncActionValue == nil || event.GetLockChatAction() == nil {
			return nil, false
		}

		chatJID := event.Index[1]
		phoneNumber := ""
		if parsedJID, err := types.ParseJID(chatJID); err == nil {
			if parsedJID.Server == types.DefaultUserServer || parsedJID.Server == types.LegacyUserServer {
				phoneNumber = parsedJID.User
			}
		}
		pushName := ""
		if lookup != nil {
			pushName = lookup.LookupContact(chatJID)
		}

		return watchdogPayload("ChatLock", map[string]interface{}{
			"jid":          chatJID,
			"timestamp":    time.UnixMilli(event.GetTimestamp()).UTC().Format(time.RFC3339Nano),
			"locked":       event.GetLockChatAction().GetLocked(),
			"fromFullSync": len(initialSync) > 0 && initialSync[0],
			"phoneNumber":  phoneNumber,
			"pushName":     pushName,
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
