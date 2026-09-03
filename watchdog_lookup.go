package main

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
	"go.mau.fi/whatsmeow/types"
)

// MessageLookup resolves historical message content and contact push names.
type MessageLookup interface {
	// LookupMessage retrieves stored information about a message by chat and message ID.
	LookupMessage(chatJID, messageID string) (senderJID, textContent string, found bool)
	// LookupContact retrieves a contact's PushName given a JID string.
	// If the JID is a LID (@lid), it first resolves it to the phone-number JID.
	LookupContact(jidStr string) string
	// MyPhoneNumber returns the account owner's phone number.
	MyPhoneNumber() string
	// MyPushName returns the account owner's push name from the session store.
	MyPushName() string
	// ResolveLID resolves a LID JID (@lid) to the real phone-number JID (@s.whatsapp.net).
	// Returns the original JID unchanged if it's not a LID or if resolution fails.
	ResolveLID(jid types.JID) types.JID
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
		MessageType string `db:"message_type"`
		TextContent string `db:"text_content"`
	}

	query := l.s.db.Rebind(`
		SELECT sender_jid, message_type, text_content
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

	textContent = row.TextContent
	if textContent == "" {
		switch row.MessageType {
		case "view_once_image", "view_once":
			textContent = ":view_once_image:"
		case "view_once_video":
			textContent = ":view_once_video:"
		case "view_once_audio":
			textContent = ":view_once_audio:"
		case "image":
			textContent = ":image:"
		case "video":
			textContent = ":video:"
		case "audio":
			textContent = ":audio:"
		case "document":
			textContent = ":document:"
		case "sticker":
			textContent = ":sticker:"
		case "contact":
			textContent = ":contact:"
		case "location":
			textContent = ":location:"
		}
	}

	return row.SenderJID, textContent, true
}

func (l *clientMessageLookup) LookupContact(jidStr string) string {
	if l == nil || l.client == nil || l.client.WAClient == nil || l.client.WAClient.Store == nil || l.client.WAClient.Store.Contacts == nil || jidStr == "" {
		return ""
	}
	parsedJID, err := types.ParseJID(jidStr)
	if err != nil {
		return ""
	}

	// If the JID is a LID, resolve it to the phone-number JID first
	// so the contact store lookup finds the entry keyed by @s.whatsapp.net.
	if parsedJID.Server == types.HiddenUserServer {
		resolved := l.ResolveLID(parsedJID)
		if resolved.Server != types.HiddenUserServer {
			parsedJID = resolved
		}
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

func (l *clientMessageLookup) MyPushName() string {
	if l == nil || l.client == nil || l.client.WAClient == nil || l.client.WAClient.Store == nil {
		return ""
	}
	return l.client.WAClient.Store.PushName
}

// ResolveLID resolves a LID JID to its phone-number JID using the whatsmeow
// LID-to-PN mapping store. Returns the original JID if it's not a LID or
// resolution fails.
func (l *clientMessageLookup) ResolveLID(jid types.JID) types.JID {
	if l == nil || l.client == nil || l.client.WAClient == nil {
		return jid
	}
	if jid.Server != types.HiddenUserServer {
		return jid
	}
	client := l.client.WAClient
	if client.Store == nil || client.Store.LIDs == nil {
		return jid
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pn, err := client.Store.LIDs.GetPNForLID(ctx, jid)
	if err != nil || pn.IsEmpty() {
		log.Debug().Err(err).Str("lid", jid.String()).Msg("Could not resolve LID to phone number")
		return jid
	}
	return pn
}

// extractPhoneNumber extracts the phone number from a JID string.
// When a lookup is provided and the JID is a LID (@lid), it resolves the LID
// to the real phone number first.
func extractPhoneNumber(jidStr string, lookup ...MessageLookup) string {
	if jidStr == "" || jidStr == "me" {
		return ""
	}
	parsed, err := types.ParseJID(jidStr)
	if err != nil {
		return jidStr
	}
	// If it's a LID and we have a lookup, resolve to real phone number.
	if parsed.Server == types.HiddenUserServer && len(lookup) > 0 && lookup[0] != nil {
		resolved := lookup[0].ResolveLID(parsed)
		if resolved.Server != types.HiddenUserServer && resolved.User != "" {
			return resolved.User
		}
	}
	if parsed.User != "" {
		return parsed.User
	}
	return jidStr
}
