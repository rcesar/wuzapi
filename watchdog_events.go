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
		chatPhoneNumber, chatPushName := resolveChatInfo(event.Info.Chat, lookup)

		actorCategory := "external_participant"
		if event.Info.IsFromMe {
			actorCategory = "company_account"
		}

		actorPhoneNumber := event.Info.Sender.User
		if event.Info.Sender.Server == types.HiddenUserServer && lookup != nil {
			resolved := lookup.ResolveLID(event.Info.Sender)
			if resolved.Server != types.HiddenUserServer && resolved.User != "" {
				actorPhoneNumber = resolved.User
			}
		}
		if event.Info.IsFromMe && lookup != nil && actorPhoneNumber == "" {
			actorPhoneNumber = lookup.MyPhoneNumber()
		}

		actorPushName := event.Info.PushName
		if event.Info.IsFromMe && lookup != nil && actorPushName == "" {
			actorPushName = lookup.MyPushName()
		}
		if actorPushName == "" && lookup != nil && !event.Info.Sender.IsEmpty() {
			actorPushName = lookup.LookupContact(event.Info.Sender.String())
		}

		senderPhoneNumber := ""
		senderPushName := ""
		messageContent := ""

		if lookup != nil {
			origSenderJID, origText, found := lookup.LookupMessage(chatJID, targetMessageID)
			if found {
				messageContent = origText
				if origSenderJID == "me" {
					senderPhoneNumber = lookup.MyPhoneNumber()
					senderPushName = lookup.MyPushName()
				} else if origSenderJID != "" {
					senderPhoneNumber = extractPhoneNumber(origSenderJID, lookup)
					senderPushName = lookup.LookupContact(origSenderJID)
				}
			}
		}

		if senderPhoneNumber == "" {
			senderPhoneNumber = actorPhoneNumber
		}
		if senderPushName == "" {
			senderPushName = actorPushName
		}

		phoneNumber := senderPhoneNumber
		if phoneNumber == "" {
			phoneNumber = actorPhoneNumber
		}

		pushName := senderPushName
		if pushName == "" {
			pushName = actorPushName
		}

		return watchdogPayload("MessageDeleted", map[string]interface{}{
			"chatJID":           chatJID,
			"chatPhoneNumber":   chatPhoneNumber,
			"chatPushName":      chatPushName,
			"messageID":         targetMessageID,
			"timestamp":         event.Info.Timestamp.Format(time.RFC3339Nano),
			"deleteType":        "for_everyone",
			"actorCategory":     actorCategory,
			"phoneNumber":       phoneNumber,
			"senderPhoneNumber": senderPhoneNumber,
			"senderPushName":    senderPushName,
			"actorPhoneNumber":  actorPhoneNumber,
			"actorPushName":     actorPushName,
			"pushName":          pushName,
			"messageContent":    messageContent,
		}), true

	case *events.DeleteForMe:
		if event.MessageID == "" || event.ChatJID.IsEmpty() {
			return nil, false
		}

		chatJID := event.ChatJID.String()
		chatPhoneNumber, chatPushName := resolveChatInfo(event.ChatJID, lookup)

		actorCategory := "company_account"
		actorPhoneNumber := ""
		actorPushName := ""
		if lookup != nil {
			actorPhoneNumber = lookup.MyPhoneNumber()
			actorPushName = lookup.MyPushName()
		}

		senderPhoneNumber := ""
		senderPushName := ""
		messageContent := ""

		if lookup != nil {
			origSenderJID, origText, found := lookup.LookupMessage(chatJID, event.MessageID)
			if found {
				messageContent = origText
				if origSenderJID == "me" {
					senderPhoneNumber = lookup.MyPhoneNumber()
					senderPushName = lookup.MyPushName()
				} else if origSenderJID != "" {
					senderPhoneNumber = extractPhoneNumber(origSenderJID, lookup)
					senderPushName = lookup.LookupContact(origSenderJID)
				}
			}
		}

		if senderPhoneNumber == "" && !event.SenderJID.IsEmpty() {
			senderJID := event.SenderJID
			if senderJID.Server == types.HiddenUserServer && lookup != nil {
				resolved := lookup.ResolveLID(senderJID)
				if resolved.Server != types.HiddenUserServer && resolved.User != "" {
					senderPhoneNumber = resolved.User
				}
			}
			if senderPhoneNumber == "" {
				senderPhoneNumber = senderJID.User
			}
			if senderPushName == "" && lookup != nil {
				senderPushName = lookup.LookupContact(event.SenderJID.String())
			}
		}

		if senderPhoneNumber == "" && event.IsFromMe && lookup != nil {
			senderPhoneNumber = lookup.MyPhoneNumber()
			if senderPushName == "" {
				senderPushName = lookup.MyPushName()
			}
		}

		if senderPushName == "" && senderPhoneNumber == actorPhoneNumber {
			senderPushName = actorPushName
		}

		phoneNumber := senderPhoneNumber
		if phoneNumber == "" {
			phoneNumber = actorPhoneNumber
		}

		pushName := senderPushName
		if pushName == "" {
			pushName = actorPushName
		}

		return watchdogPayload("MessageDeleted", map[string]interface{}{
			"chatJID":           chatJID,
			"chatPhoneNumber":   chatPhoneNumber,
			"chatPushName":      chatPushName,
			"messageID":         event.MessageID,
			"timestamp":         event.Timestamp.Format(time.RFC3339Nano),
			"deleteType":        "for_me",
			"actorCategory":     actorCategory,
			"fromFullSync":      event.FromFullSync,
			"phoneNumber":       phoneNumber,
			"senderPhoneNumber": senderPhoneNumber,
			"senderPushName":    senderPushName,
			"actorPhoneNumber":  actorPhoneNumber,
			"actorPushName":     actorPushName,
			"pushName":          pushName,
			"messageContent":    messageContent,
		}), true

	case *events.Archive:
		if event.Action == nil || event.JID.IsEmpty() {
			return nil, false
		}

		chatJID := event.JID.String()
		chatPhoneNumber, chatPushName := resolveChatInfo(event.JID, lookup)
		phoneNumber := chatPhoneNumber
		pushName := chatPushName

		return watchdogPayload("ChatArchive", map[string]interface{}{
			"jid":             chatJID,
			"chatPhoneNumber": chatPhoneNumber,
			"chatPushName":    chatPushName,
			"timestamp":       event.Timestamp.Format(time.RFC3339Nano),
			"archived":        event.Action.GetArchived(),
			"fromFullSync":    event.FromFullSync,
			"phoneNumber":     phoneNumber,
			"pushName":        pushName,
		}), true

	case *events.AppState:
		if len(event.Index) < 2 || event.Index[0] != appstate.IndexLock || event.SyncActionValue == nil || event.GetLockChatAction() == nil {
			return nil, false
		}

		chatJID := event.Index[1]
		chatPhoneNumber := ""
		chatPushName := ""
		if parsedJID, err := types.ParseJID(chatJID); err == nil {
			chatPhoneNumber, chatPushName = resolveChatInfo(parsedJID, lookup)
		} else if lookup != nil {
			chatPushName = lookup.LookupContact(chatJID)
		}
		phoneNumber := chatPhoneNumber
		pushName := chatPushName

		return watchdogPayload("ChatLock", map[string]interface{}{
			"jid":             chatJID,
			"chatPhoneNumber": chatPhoneNumber,
			"chatPushName":    chatPushName,
			"timestamp":       time.UnixMilli(event.GetTimestamp()).UTC().Format(time.RFC3339Nano),
			"locked":          event.GetLockChatAction().GetLocked(),
			"fromFullSync":    len(initialSync) > 0 && initialSync[0],
			"phoneNumber":     phoneNumber,
			"pushName":        pushName,
		}), true
	}

	return nil, false
}

// resolveChatInfo extracts the direct phone number and push name of a chat,
// resolving LID JIDs to real user phone numbers if available.
func resolveChatInfo(chatJID types.JID, lookup MessageLookup) (chatPhoneNumber, chatPushName string) {
	if chatJID.IsEmpty() {
		return "", ""
	}
	resolvedJID := chatJID
	if chatJID.Server == types.HiddenUserServer && lookup != nil {
		resolved := lookup.ResolveLID(chatJID)
		if resolved.Server != types.HiddenUserServer && !resolved.IsEmpty() {
			resolvedJID = resolved
		}
	}

	if resolvedJID.Server == types.DefaultUserServer || resolvedJID.Server == types.LegacyUserServer {
		chatPhoneNumber = resolvedJID.User
	}

	if lookup != nil {
		chatPushName = lookup.LookupContact(chatJID.String())
		if chatPushName == "" && resolvedJID != chatJID {
			chatPushName = lookup.LookupContact(resolvedJID.String())
		}
	}
	return chatPhoneNumber, chatPushName
}

func watchdogPayload(eventType string, event map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"contractVersion": 1,
		"type":            eventType,
		"event":           event,
	}
}
