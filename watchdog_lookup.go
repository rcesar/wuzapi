package main

import (
	"context"
	"database/sql"
	"errors"

	"github.com/rs/zerolog/log"
	"go.mau.fi/whatsmeow/types"
)

// MessageLookup resolves historical message content and contact push names.
type MessageLookup interface {
	// LookupMessage retrieves stored information about a message by chat and message ID.
	LookupMessage(chatJID, messageID string) (senderJID, textContent string, found bool)
	// LookupContact retrieves a contact's PushName given a JID string.
	LookupContact(jidStr string) string
	// MyPhoneNumber returns the account owner's phone number.
	MyPhoneNumber() string
}

type clientMessageLookup struct {
	s      *server
	client *MyClient
	userID string
}

func (l *clientMessageLookup) LookupMessage(chatJID, messageID string) (senderJID, textContent string, found bool) {
	if l == nil || l.s == nil || l.s.db == nil || messageID == "" {
		return "", "", false
	}

	var row struct {
		SenderJID   string `db:"sender_jid"`
		TextContent string `db:"text_content"`
	}

	query := l.s.db.Rebind(`
		SELECT sender_jid, text_content
		FROM message_history
		WHERE user_id = ? AND message_id = ?
		LIMIT 1
	`)

	err := l.s.db.Get(&row, query, l.userID, messageID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Warn().Err(err).Str("messageID", messageID).Msg("Failed to lookup message from history")
		}
		return "", "", false
	}

	return row.SenderJID, row.TextContent, true
}

func (l *clientMessageLookup) LookupContact(jidStr string) string {
	if l == nil || l.client == nil || l.client.WAClient == nil || l.client.WAClient.Store == nil || l.client.WAClient.Store.Contacts == nil || jidStr == "" {
		return ""
	}
	parsedJID, err := types.ParseJID(jidStr)
	if err != nil {
		return ""
	}
	contact, err := l.client.WAClient.Store.Contacts.GetContact(context.Background(), parsedJID)
	if err != nil {
		return ""
	}
	if contact.PushName != "" {
		return contact.PushName
	}
	if contact.FullName != "" {
		return contact.FullName
	}
	if contact.BusinessName != "" {
		return contact.BusinessName
	}
	return ""
}

func (l *clientMessageLookup) MyPhoneNumber() string {
	if l == nil || l.client == nil || l.client.WAClient == nil || l.client.WAClient.Store == nil || l.client.WAClient.Store.ID == nil {
		return ""
	}
	return l.client.WAClient.Store.ID.User
}

func extractPhoneNumber(jidStr string) string {
	if jidStr == "" || jidStr == "me" {
		return ""
	}
	parsed, err := types.ParseJID(jidStr)
	if err == nil && parsed.User != "" {
		return parsed.User
	}
	return jidStr
}
