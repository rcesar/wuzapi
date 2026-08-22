package main

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func mustJID(t *testing.T, value string) types.JID {
	t.Helper()
	jid, err := types.ParseJID(value)
	if err != nil {
		t.Fatalf("parse jid: %v", err)
	}
	return jid
}

func TestWatchdogEventPayload(t *testing.T) {
	chat := mustJID(t, "5511999999999@s.whatsapp.net")
	when := time.Date(2026, 8, 21, 14, 0, 0, 0, time.UTC)

	t.Run("delete for everyone", func(t *testing.T) {
		evt := &events.Message{
			Info: types.MessageInfo{
				MessageSource: types.MessageSource{Chat: chat, Sender: chat, IsFromMe: false},
				ID:            "DELETE-EVENT-1",
				Timestamp:     when,
			},
			Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
				Type: waE2E.ProtocolMessage_REVOKE.Enum(),
				Key:  &waCommon.MessageKey{ID: proto.String("ORIGINAL-1")},
			}},
		}

		payload, ok := watchdogEventPayload(evt)
		if !ok || payload["type"] != "MessageDeleted" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["messageID"] != "ORIGINAL-1" || event["actorCategory"] != "external_participant" {
			t.Fatalf("unexpected deletion event: %#v", event)
		}
	})

	t.Run("delete for me", func(t *testing.T) {
		payload, ok := watchdogEventPayload(&events.DeleteForMe{
			ChatJID: chat, MessageID: "ORIGINAL-2", Timestamp: when,
		})
		if !ok || payload["type"] != "MessageDeleted" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["deleteType"] != "for_me" || event["actorCategory"] != "company_account" {
			t.Fatalf("unexpected delete-for-me event: %#v", event)
		}
	})

	t.Run("archive", func(t *testing.T) {
		payload, ok := watchdogEventPayload(&events.Archive{
			JID: chat, Timestamp: when, FromFullSync: true,
			Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(true)},
		})
		if !ok || payload["type"] != "ChatArchive" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["archived"] != true || event["fromFullSync"] != true {
			t.Fatalf("unexpected archive event: %#v", event)
		}
	})

	t.Run("chat privacy lock", func(t *testing.T) {
		payload, ok := watchdogEventPayload(&events.AppState{
			Index: []string{appstate.IndexLock, chat.String()},
			SyncActionValue: &waSyncAction.SyncActionValue{
				Timestamp:      proto.Int64(when.UnixMilli()),
				LockChatAction: &waSyncAction.LockChatAction{Locked: proto.Bool(true)},
			},
		}, true)
		if !ok || payload["type"] != "ChatLock" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["locked"] != true || event["jid"] != chat.String() || event["fromFullSync"] != true {
			t.Fatalf("unexpected lock event: %#v", event)
		}
	})
}
