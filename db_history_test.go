package main

import (
	"testing"
)

// TestSaveMessageToHistoryIdempotent verifies the fix for #292: persisting a
// message whose (user_id, message_id) already exists must NOT return an error
// (the plain INSERT previously violated the message_history unique constraint
// and was logged at ERROR on every HistorySync), and must not create a
// duplicate row.
func TestSaveMessageToHistoryIdempotent(t *testing.T) {
	s := makeTestServer(t)

	const (
		userID = "user-1"
		chat   = "123456@s.whatsapp.net"
		sender = "123456@s.whatsapp.net"
		msgID  = "MSG-DUP-1"
	)

	// First insert: a normal live Message event.
	if err := s.saveMessageToHistory(userID, chat, sender, msgID, "text", "hello", "", "", "{}"); err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	// Second insert with the same (user_id, message_id): simulates the same
	// message arriving again in a HistorySync batch or on reconnect. With the
	// fix this is a silent no-op; without it, it returns a unique-constraint
	// violation.
	if err := s.saveMessageToHistory(userID, chat, sender, msgID, "text", "hello", "", "", "{}"); err != nil {
		t.Fatalf("duplicate insert should be a silent no-op, got error: %v", err)
	}

	// Exactly one row must exist for this (user_id, message_id).
	var count int
	if err := s.db.Get(&count,
		"SELECT COUNT(*) FROM message_history WHERE user_id = ? AND message_id = ?",
		userID, msgID); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row after duplicate insert, got %d", count)
	}

	// A different message_id for the same user must still insert normally
	// (the conflict clause must not swallow legitimate inserts).
	if err := s.saveMessageToHistory(userID, chat, sender, "MSG-OTHER", "text", "world", "", "", "{}"); err != nil {
		t.Fatalf("insert of a distinct message failed: %v", err)
	}
	if err := s.db.Get(&count,
		"SELECT COUNT(*) FROM message_history WHERE user_id = ?", userID); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 distinct rows, got %d", count)
	}
}

func TestLookupMessageFallbackWhenTextContentEmpty(t *testing.T) {
	s := makeTestServer(t)

	const (
		userID = "user-lookup"
		chat   = "123456@s.whatsapp.net"
		sender = "123456@s.whatsapp.net"
	)

	lookup := &clientMessageLookup{s: s, userID: userID}

	// 1. Image with empty text_content (e.g. from API or view-once)
	if err := s.saveMessageToHistory(userID, chat, sender, "MSG-IMG-1", "image", "", "", "", "{}"); err != nil {
		t.Fatalf("insert image failed: %v", err)
	}
	_, text, found := lookup.LookupMessage(chat, "MSG-IMG-1")
	if !found || text != ":image:" {
		t.Errorf("expected found=true, text=':image:'; got found=%v, text=%q", found, text)
	}

	// 2. Video with empty text_content
	if err := s.saveMessageToHistory(userID, chat, sender, "MSG-VID-1", "video", "", "", "", "{}"); err != nil {
		t.Fatalf("insert video failed: %v", err)
	}
	_, text, found = lookup.LookupMessage(chat, "MSG-VID-1")
	if !found || text != ":video:" {
		t.Errorf("expected found=true, text=':video:'; got found=%v, text=%q", found, text)
	}

	// 3. Audio with empty text_content
	if err := s.saveMessageToHistory(userID, chat, sender, "MSG-AUD-1", "audio", "", "", "", "{}"); err != nil {
		t.Fatalf("insert audio failed: %v", err)
	}
	_, text, found = lookup.LookupMessage(chat, "MSG-AUD-1")
	if !found || text != ":audio:" {
		t.Errorf("expected found=true, text=':audio:'; got found=%v, text=%q", found, text)
	}

	// 4. Media with caption: should keep caption
	if err := s.saveMessageToHistory(userID, chat, sender, "MSG-CAP-1", "image", "foto da receita", "", "", "{}"); err != nil {
		t.Fatalf("insert image with caption failed: %v", err)
	}
	_, text, found = lookup.LookupMessage(chat, "MSG-CAP-1")
	if !found || text != "foto da receita" {
		t.Errorf("expected found=true, text='foto da receita'; got found=%v, text=%q", found, text)
	}
}

func TestUndecryptableViewOnceHistoryAndLookup(t *testing.T) {
	s := makeTestServer(t)

	const (
		userID = "user-undecryptable"
		chat   = "262955211948064@lid"
		sender = "me"
		msgID  = "2A417B6A7DFBB4197E96"
	)

	lookup := &clientMessageLookup{s: s, userID: userID}

	// Simulates saving UndecryptableMessage with view_once
	if err := s.saveMessageToHistory(userID, chat, sender, msgID, "image", ":image:", "", "", "{}"); err != nil {
		t.Fatalf("insert undecryptable view-once failed: %v", err)
	}

	origSender, text, found := lookup.LookupMessage(chat, msgID)
	if !found {
		t.Fatalf("expected message to be found in history")
	}
	if origSender != "me" {
		t.Errorf("expected origSender 'me', got %q", origSender)
	}
	if text != ":image:" {
		t.Errorf("expected text ':image:', got %q", text)
	}
}
