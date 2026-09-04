package main

import (
	"os"
	"strings"
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

type mockLookup struct {
	messages   map[string]struct{ sender, text string }
	contacts   map[string]string
	myPhone    string
	myPushName string
	lidMap     map[string]types.JID // LID string → resolved phone-number JID
}

func (m *mockLookup) LookupMessage(chatJID, messageID string) (string, string, bool) {
	if m.messages != nil {
		if val, ok := m.messages[messageID]; ok {
			return val.sender, val.text, true
		}
	}
	return "", "", false
}

func (m *mockLookup) LookupContact(jid string) string {
	if m.contacts != nil {
		// Mirror real implementation: resolve LID before contact lookup.
		if m.lidMap != nil {
			if parsedJID, err := types.ParseJID(jid); err == nil && parsedJID.Server == types.HiddenUserServer {
				if resolved, ok := m.lidMap[parsedJID.String()]; ok {
					jid = resolved.String()
				}
			}
		}
		return m.contacts[jid]
	}
	return ""
}

func (m *mockLookup) MyPhoneNumber() string {
	return m.myPhone
}

func (m *mockLookup) MyPushName() string {
	return m.myPushName
}

func (m *mockLookup) ResolveLID(jid types.JID) types.JID {
	if m.lidMap != nil {
		if resolved, ok := m.lidMap[jid.String()]; ok {
			return resolved
		}
	}
	return jid
}

func TestWatchdogSubscriptionEventsAreSupported(t *testing.T) {
	requiredEvents := []string{
		"Message",
		"HistorySync",
		"Connected",
		"Disconnected",
		"ConnectFailure",
		"KeepAliveRestored",
		"LoggedOut",
		"MessageDeleted",
		"ChatArchive",
		"ChatClear",
		"ChatDelete",
		"ChatLock",
		"ChatMute",
	}

	for _, eventType := range requiredEvents {
		if !isValidEventType(eventType) {
			t.Errorf("Watchdog subscription event %q is not supported", eventType)
		}
	}
}

func TestWatchdogEventsInDashboardUI(t *testing.T) {
	content, err := os.ReadFile("static/dashboard/index.html")
	if err != nil {
		t.Fatalf("failed to read static/dashboard/index.html: %v", err)
	}
	html := string(content)

	requiredEvents := []string{
		"MessageDeleted",
		"ChatArchive",
		"ChatClear",
		"ChatDelete",
		"ChatLock",
		"ChatMute",
		"ReadReceipt",
	}

	for _, evt := range requiredEvents {
		expectedOption := `<option value="` + evt + `"`
		count := strings.Count(html, expectedOption)
		if count < 2 {
			t.Errorf("expected at least 2 instances of %q in dashboard index.html (webhookEvents and webhookEventsInstance), found %d", expectedOption, count)
		}
	}

	loginContent, err := os.ReadFile("static/login/index.html")
	if err != nil {
		t.Fatalf("failed to read static/login/index.html: %v", err)
	}
	loginHtml := string(loginContent)
	for _, evt := range requiredEvents {
		expectedOption := `<option value="` + evt + `"`
		if !strings.Contains(loginHtml, expectedOption) {
			t.Errorf("expected %q in login index.html", expectedOption)
		}
	}
}

func TestWatchdogEventPayload(t *testing.T) {
	chat := mustJID(t, "5511999999999@s.whatsapp.net")
	when := time.Date(2026, 8, 21, 14, 0, 0, 0, time.UTC)

	t.Run("delete for everyone without lookup", func(t *testing.T) {
		evt := &events.Message{
			Info: types.MessageInfo{
				MessageSource: types.MessageSource{Chat: chat, Sender: chat, IsFromMe: false},
				ID:            "DELETE-EVENT-1",
				Timestamp:     when,
				PushName:      "Actor Push",
			},
			Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
				Type: waE2E.ProtocolMessage_REVOKE.Enum(),
				Key:  &waCommon.MessageKey{ID: proto.String("ORIGINAL-1")},
			}},
		}

		payload, ok := watchdogEventPayload(evt, nil)
		if !ok || payload["type"] != "MessageDeleted" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["messageID"] != "ORIGINAL-1" || event["actorCategory"] != "external_participant" {
			t.Fatalf("unexpected deletion event: %#v", event)
		}
		if event["phoneNumber"] != "5511999999999" || event["actorPhoneNumber"] != "5511999999999" {
			t.Fatalf("unexpected phone numbers: %#v", event)
		}
		if event["pushName"] != "Actor Push" {
			t.Fatalf("expected pushName 'Actor Push', got: %#v", event["pushName"])
		}
		if event["actorPushName"] != "Actor Push" {
			t.Fatalf("expected actorPushName 'Actor Push', got: %#v", event["actorPushName"])
		}
		if event["senderPushName"] != "Actor Push" {
			t.Fatalf("expected senderPushName 'Actor Push', got: %#v", event["senderPushName"])
		}
		if event["messageContent"] != "" {
			t.Fatalf("expected empty messageContent without lookup, got: %#v", event["messageContent"])
		}
	})

	t.Run("delete for everyone with lookup enrichment", func(t *testing.T) {
		evt := &events.Message{
			Info: types.MessageInfo{
				MessageSource: types.MessageSource{Chat: chat, Sender: chat, IsFromMe: false},
				ID:            "DELETE-EVENT-2",
				Timestamp:     when,
			},
			Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
				Type: waE2E.ProtocolMessage_REVOKE.Enum(),
				Key:  &waCommon.MessageKey{ID: proto.String("ORIGINAL-ENRICHED")},
			}},
		}

		mock := &mockLookup{
			messages: map[string]struct{ sender, text string }{
				"ORIGINAL-ENRICHED": {
					sender: "5511888888888@s.whatsapp.net",
					text:   "Mensagem secreta original",
				},
			},
			contacts: map[string]string{
				"5511888888888@s.whatsapp.net": "Contato Original",
				chat.String():                  "Chat Contact",
			},
			myPhone:    "5511000000000",
			myPushName: "My Account",
		}

		payload, ok := watchdogEventPayload(evt, mock)
		if !ok || payload["type"] != "MessageDeleted" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["messageContent"] != "Mensagem secreta original" {
			t.Fatalf("expected enriched messageContent, got: %#v", event["messageContent"])
		}
		if event["senderPhoneNumber"] != "5511888888888" {
			t.Fatalf("expected senderPhoneNumber '5511888888888', got: %#v", event["senderPhoneNumber"])
		}
		if event["senderPushName"] != "Contato Original" {
			t.Fatalf("expected senderPushName 'Contato Original', got: %#v", event["senderPushName"])
		}
		if event["pushName"] != "Contato Original" {
			t.Fatalf("expected pushName 'Contato Original', got: %#v", event["pushName"])
		}
		if event["actorPushName"] != "Chat Contact" {
			t.Fatalf("expected actorPushName 'Chat Contact', got: %#v", event["actorPushName"])
		}
	})

	t.Run("delete for me with lookup enrichment", func(t *testing.T) {
		mock := &mockLookup{
			messages: map[string]struct{ sender, text string }{
				"ORIGINAL-2": {
					sender: "5511777777777@s.whatsapp.net",
					text:   "Texto antes de apagar para mim",
				},
			},
			contacts: map[string]string{
				"5511777777777@s.whatsapp.net": "Maria Silva",
			},
			myPhone:    "5511000000000",
			myPushName: "Vivva Admin",
		}

		payload, ok := watchdogEventPayload(&events.DeleteForMe{
			ChatJID: chat, MessageID: "ORIGINAL-2", Timestamp: when,
		}, mock)
		if !ok || payload["type"] != "MessageDeleted" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["deleteType"] != "for_me" || event["actorCategory"] != "company_account" {
			t.Fatalf("unexpected delete-for-me event: %#v", event)
		}
		if event["messageContent"] != "Texto antes de apagar para mim" {
			t.Fatalf("expected messageContent enriched, got: %#v", event["messageContent"])
		}
		if event["senderPhoneNumber"] != "5511777777777" {
			t.Fatalf("expected senderPhoneNumber '5511777777777', got: %#v", event["senderPhoneNumber"])
		}
		if event["senderPushName"] != "Maria Silva" {
			t.Fatalf("expected senderPushName 'Maria Silva', got: %#v", event["senderPushName"])
		}
		if event["actorPhoneNumber"] != "5511000000000" {
			t.Fatalf("expected actorPhoneNumber '5511000000000', got: %#v", event["actorPhoneNumber"])
		}
		if event["actorPushName"] != "Vivva Admin" {
			t.Fatalf("expected actorPushName 'Vivva Admin', got: %#v", event["actorPushName"])
		}
		if event["pushName"] != "Maria Silva" {
			t.Fatalf("expected pushName 'Maria Silva', got: %#v", event["pushName"])
		}
	})

	t.Run("archive with phone number and push name", func(t *testing.T) {
		mock := &mockLookup{
			contacts: map[string]string{
				chat.String(): "João Santos",
			},
		}

		payload, ok := watchdogEventPayload(&events.Archive{
			JID: chat, Timestamp: when, FromFullSync: true,
			Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(true)},
		}, mock)
		if !ok || payload["type"] != "ChatArchive" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["archived"] != true || event["fromFullSync"] != true {
			t.Fatalf("unexpected archive event: %#v", event)
		}
		if event["phoneNumber"] != "5511999999999" {
			t.Fatalf("expected phoneNumber '5511999999999', got: %#v", event["phoneNumber"])
		}
		if event["pushName"] != "João Santos" {
			t.Fatalf("expected pushName 'João Santos', got: %#v", event["pushName"])
		}
	})

	t.Run("chat privacy lock with phone number and push name", func(t *testing.T) {
		mock := &mockLookup{
			contacts: map[string]string{
				chat.String(): "Carlos Souza",
			},
		}

		payload, ok := watchdogEventPayload(&events.AppState{
			Index: []string{appstate.IndexLock, chat.String()},
			SyncActionValue: &waSyncAction.SyncActionValue{
				Timestamp:      proto.Int64(when.UnixMilli()),
				LockChatAction: &waSyncAction.LockChatAction{Locked: proto.Bool(true)},
			},
		}, mock, true)
		if !ok || payload["type"] != "ChatLock" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["locked"] != true || event["jid"] != chat.String() || event["fromFullSync"] != true {
			t.Fatalf("unexpected lock event: %#v", event)
		}
		if event["phoneNumber"] != "5511999999999" {
			t.Fatalf("expected phoneNumber '5511999999999', got: %#v", event["phoneNumber"])
		}
		if event["pushName"] != "Carlos Souza" {
			t.Fatalf("expected pushName 'Carlos Souza', got: %#v", event["pushName"])
		}
	})

	t.Run("delete for me with LID resolves to real phone number", func(t *testing.T) {
		lidJID := mustJID(t, "262955211948064@lid")
		resolvedJID := mustJID(t, "5511888888888@s.whatsapp.net")

		mock := &mockLookup{
			messages: map[string]struct{ sender, text string }{
				"LID-MSG-1": {
					sender: "262955211948064@lid",
					text:   "Boa tarde tudo bem?",
				},
			},
			contacts: map[string]string{
				"5511888888888@s.whatsapp.net": "Fulano da Silva",
			},
			myPhone:    "5512996754791",
			myPushName: "Vivva Laboratorio",
			lidMap: map[string]types.JID{
				lidJID.String(): resolvedJID,
			},
		}

		payload, ok := watchdogEventPayload(&events.DeleteForMe{
			ChatJID: lidJID, MessageID: "LID-MSG-1", Timestamp: when,
		}, mock)
		if !ok || payload["type"] != "MessageDeleted" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["senderPhoneNumber"] != "5511888888888" {
			t.Fatalf("expected senderPhoneNumber '5511888888888', got: %#v", event["senderPhoneNumber"])
		}
		if event["senderPushName"] != "Fulano da Silva" {
			t.Fatalf("expected senderPushName 'Fulano da Silva', got: %#v", event["senderPushName"])
		}
		if event["actorPushName"] != "Vivva Laboratorio" {
			t.Fatalf("expected actorPushName 'Vivva Laboratorio', got: %#v", event["actorPushName"])
		}
		if event["phoneNumber"] != "5511888888888" {
			t.Fatalf("expected phoneNumber '5511888888888', got: %#v", event["phoneNumber"])
		}
		if event["pushName"] != "Fulano da Silva" {
			t.Fatalf("expected pushName 'Fulano da Silva', got: %#v", event["pushName"])
		}
		if event["chatPhoneNumber"] != "5511888888888" {
			t.Fatalf("expected chatPhoneNumber '5511888888888', got: %#v", event["chatPhoneNumber"])
		}
		if event["chatPushName"] != "Fulano da Silva" {
			t.Fatalf("expected chatPushName 'Fulano da Silva', got: %#v", event["chatPushName"])
		}
		if event["messageContent"] != "Boa tarde tudo bem?" {
			t.Fatalf("expected messageContent 'Boa tarde tudo bem?', got: %#v", event["messageContent"])
		}
	})

	t.Run("delete for me of own message in LID chat identifies chat partner", func(t *testing.T) {
		lidJID := mustJID(t, "262955211948064@lid")
		resolvedJID := mustJID(t, "5511987654321@s.whatsapp.net")

		mock := &mockLookup{
			messages: map[string]struct{ sender, text string }{
				"2AEC5D3A4116DE6C4520": {
					sender: "me",
					text:   "I’m good and you?",
				},
			},
			contacts: map[string]string{
				"5511987654321@s.whatsapp.net": "Cliente Importante",
			},
			myPhone:    "5512996754791",
			myPushName: "Vivva Laboratorio",
			lidMap: map[string]types.JID{
				lidJID.String(): resolvedJID,
			},
		}

		payload, ok := watchdogEventPayload(&events.DeleteForMe{
			ChatJID: lidJID, MessageID: "2AEC5D3A4116DE6C4520", Timestamp: when, IsFromMe: true,
		}, mock)
		if !ok || payload["type"] != "MessageDeleted" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		// Monitored account sent and deleted
		if event["actorPhoneNumber"] != "5512996754791" {
			t.Fatalf("expected actorPhoneNumber '5512996754791', got: %#v", event["actorPhoneNumber"])
		}
		if event["actorPushName"] != "Vivva Laboratorio" {
			t.Fatalf("expected actorPushName 'Vivva Laboratorio', got: %#v", event["actorPushName"])
		}
		if event["senderPhoneNumber"] != "5512996754791" {
			t.Fatalf("expected senderPhoneNumber '5512996754791', got: %#v", event["senderPhoneNumber"])
		}
		if event["senderPushName"] != "Vivva Laboratorio" {
			t.Fatalf("expected senderPushName 'Vivva Laboratorio', got: %#v", event["senderPushName"])
		}
		if event["phoneNumber"] != "5512996754791" {
			t.Fatalf("expected phoneNumber '5512996754791', got: %#v", event["phoneNumber"])
		}
		if event["pushName"] != "Vivva Laboratorio" {
			t.Fatalf("expected pushName 'Vivva Laboratorio', got: %#v", event["pushName"])
		}
		// Interlocutor chat info clearly resolved
		if event["chatPhoneNumber"] != "5511987654321" {
			t.Fatalf("expected chatPhoneNumber '5511987654321', got: %#v", event["chatPhoneNumber"])
		}
		if event["chatPushName"] != "Cliente Importante" {
			t.Fatalf("expected chatPushName 'Cliente Importante', got: %#v", event["chatPushName"])
		}
		if event["messageContent"] != "I’m good and you?" {
			t.Fatalf("expected messageContent 'I’m good and you?', got: %#v", event["messageContent"])
		}
	})

	t.Run("archive with LID resolves phone number", func(t *testing.T) {
		lidJID := mustJID(t, "262955211948064@lid")
		resolvedJID := mustJID(t, "5511888888888@s.whatsapp.net")

		mock := &mockLookup{
			contacts: map[string]string{
				"5511888888888@s.whatsapp.net": "João LID",
			},
			lidMap: map[string]types.JID{
				lidJID.String(): resolvedJID,
			},
		}

		payload, ok := watchdogEventPayload(&events.Archive{
			JID: lidJID, Timestamp: when,
			Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(true)},
		}, mock)
		if !ok || payload["type"] != "ChatArchive" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["phoneNumber"] != "5511888888888" {
			t.Fatalf("expected phoneNumber '5511888888888', got: %#v", event["phoneNumber"])
		}
		if event["chatPhoneNumber"] != "5511888888888" {
			t.Fatalf("expected chatPhoneNumber '5511888888888', got: %#v", event["chatPhoneNumber"])
		}
		if event["pushName"] != "João LID" {
			t.Fatalf("expected pushName 'João LID', got: %#v", event["pushName"])
		}
		if event["chatPushName"] != "João LID" {
			t.Fatalf("expected chatPushName 'João LID', got: %#v", event["chatPushName"])
		}
	})

	t.Run("delete for me of view once image in LID chat identifies content and chat partner", func(t *testing.T) {
		lidJID := mustJID(t, "262955211948064@lid")
		contactJID := mustJID(t, "5512982698373@s.whatsapp.net")

		mock := &mockLookup{
			messages: map[string]struct{ sender, text string }{
				"2AD4FB9167D5152218B6": {
					sender: "me",
					text:   ":view_once_image:",
				},
			},
			contacts: map[string]string{
				contactJID.String(): "Renan Cesar",
			},
			lidMap: map[string]types.JID{
				lidJID.String(): contactJID,
			},
			myPhone:    "5512996754791",
			myPushName: "Vivva Laboratorio",
		}

		payload, ok := watchdogEventPayload(&events.DeleteForMe{
			ChatJID:   lidJID,
			MessageID: "2AD4FB9167D5152218B6",
			Timestamp: when,
			IsFromMe:  true,
		}, mock)
		if !ok || payload["type"] != "MessageDeleted" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["deleteType"] != "for_me" || event["actorCategory"] != "company_account" {
			t.Fatalf("unexpected delete-for-me event: %#v", event)
		}
		if event["messageID"] != "2AD4FB9167D5152218B6" {
			t.Fatalf("expected messageID '2AD4FB9167D5152218B6', got: %#v", event["messageID"])
		}
		if event["messageContent"] != ":view_once_image:" {
			t.Fatalf("expected messageContent ':view_once_image:', got: %#v", event["messageContent"])
		}
		if event["actorPhoneNumber"] != "5512996754791" {
			t.Fatalf("expected actorPhoneNumber '5512996754791', got: %#v", event["actorPhoneNumber"])
		}
		if event["actorPushName"] != "Vivva Laboratorio" {
			t.Fatalf("expected actorPushName 'Vivva Laboratorio', got: %#v", event["actorPushName"])
		}
		if event["senderPhoneNumber"] != "5512996754791" {
			t.Fatalf("expected senderPhoneNumber '5512996754791', got: %#v", event["senderPhoneNumber"])
		}
		if event["senderPushName"] != "Vivva Laboratorio" {
			t.Fatalf("expected senderPushName 'Vivva Laboratorio', got: %#v", event["senderPushName"])
		}
		if event["chatPhoneNumber"] != "5512982698373" {
			t.Fatalf("expected chatPhoneNumber '5512982698373', got: %#v", event["chatPhoneNumber"])
		}
		if event["chatPushName"] != "Renan Cesar" {
			t.Fatalf("expected chatPushName 'Renan Cesar', got: %#v", event["chatPushName"])
		}
	})

	t.Run("clear chat with phone number and push name", func(t *testing.T) {
		mock := &mockLookup{
			contacts: map[string]string{
				chat.String(): "João Santos",
			},
		}

		payload, ok := watchdogEventPayload(&events.ClearChat{
			JID: chat, Timestamp: when, FromFullSync: false, DeleteMedia: true,
		}, mock)
		if !ok || payload["type"] != "ChatClear" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["fromFullSync"] != false {
			t.Fatalf("expected fromFullSync false, got: %#v", event["fromFullSync"])
		}
		if event["deleteMedia"] != true {
			t.Fatalf("expected deleteMedia true, got: %#v", event["deleteMedia"])
		}
		if event["phoneNumber"] != "5511999999999" {
			t.Fatalf("expected phoneNumber '5511999999999', got: %#v", event["phoneNumber"])
		}
		if event["pushName"] != "João Santos" {
			t.Fatalf("expected pushName 'João Santos', got: %#v", event["pushName"])
		}
	})

	t.Run("clear chat with LID resolves phone number", func(t *testing.T) {
		lidJID := mustJID(t, "262955211948064@lid")
		resolvedJID := mustJID(t, "5511888888888@s.whatsapp.net")

		mock := &mockLookup{
			contacts: map[string]string{
				"5511888888888@s.whatsapp.net": "Maria LID",
			},
			lidMap: map[string]types.JID{
				lidJID.String(): resolvedJID,
			},
		}

		payload, ok := watchdogEventPayload(&events.ClearChat{
			JID: lidJID, Timestamp: when,
		}, mock)
		if !ok || payload["type"] != "ChatClear" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["phoneNumber"] != "5511888888888" {
			t.Fatalf("expected phoneNumber '5511888888888', got: %#v", event["phoneNumber"])
		}
		if event["chatPhoneNumber"] != "5511888888888" {
			t.Fatalf("expected chatPhoneNumber '5511888888888', got: %#v", event["chatPhoneNumber"])
		}
		if event["pushName"] != "Maria LID" {
			t.Fatalf("expected pushName 'Maria LID', got: %#v", event["pushName"])
		}
	})

	t.Run("delete chat with phone number and push name", func(t *testing.T) {
		mock := &mockLookup{
			contacts: map[string]string{
				chat.String(): "Carlos Souza",
			},
		}

		payload, ok := watchdogEventPayload(&events.DeleteChat{
			JID: chat, Timestamp: when, FromFullSync: false, DeleteMedia: true,
		}, mock)
		if !ok || payload["type"] != "ChatDelete" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["deleteMedia"] != true {
			t.Fatalf("expected deleteMedia true, got: %#v", event["deleteMedia"])
		}
		if event["phoneNumber"] != "5511999999999" {
			t.Fatalf("expected phoneNumber '5511999999999', got: %#v", event["phoneNumber"])
		}
		if event["pushName"] != "Carlos Souza" {
			t.Fatalf("expected pushName 'Carlos Souza', got: %#v", event["pushName"])
		}
	})

	t.Run("mute with phone number and push name", func(t *testing.T) {
		mock := &mockLookup{
			contacts: map[string]string{
				chat.String(): "Ana Silva",
			},
		}

		muteEnd := int64(1756310400) // some future timestamp
		payload, ok := watchdogEventPayload(&events.Mute{
			JID: chat, Timestamp: when, FromFullSync: false,
			Action: &waSyncAction.MuteAction{Muted: proto.Bool(true), MuteEndTimestamp: &muteEnd},
		}, mock)
		if !ok || payload["type"] != "ChatMute" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["muted"] != true {
			t.Fatalf("expected muted true, got: %#v", event["muted"])
		}
		if event["muteEndTimestamp"] == "" {
			t.Fatalf("expected muteEndTimestamp to be set, got empty")
		}
		if event["phoneNumber"] != "5511999999999" {
			t.Fatalf("expected phoneNumber '5511999999999', got: %#v", event["phoneNumber"])
		}
		if event["pushName"] != "Ana Silva" {
			t.Fatalf("expected pushName 'Ana Silva', got: %#v", event["pushName"])
		}
	})

	t.Run("unmute with phone number and push name", func(t *testing.T) {
		mock := &mockLookup{
			contacts: map[string]string{
				chat.String(): "Pedro Lima",
			},
		}

		payload, ok := watchdogEventPayload(&events.Mute{
			JID: chat, Timestamp: when, FromFullSync: false,
			Action: &waSyncAction.MuteAction{Muted: proto.Bool(false)},
		}, mock)
		if !ok || payload["type"] != "ChatMute" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		event := payload["event"].(map[string]interface{})
		if event["muted"] != false {
			t.Fatalf("expected muted false, got: %#v", event["muted"])
		}
		if event["muteEndTimestamp"] != "" {
			t.Fatalf("expected muteEndTimestamp empty for unmute, got: %#v", event["muteEndTimestamp"])
		}
		if event["phoneNumber"] != "5511999999999" {
			t.Fatalf("expected phoneNumber '5511999999999', got: %#v", event["phoneNumber"])
		}
		if event["pushName"] != "Pedro Lima" {
			t.Fatalf("expected pushName 'Pedro Lima', got: %#v", event["pushName"])
		}
	})
}
