package main

import (
	"go.mau.fi/whatsmeow/proto/waE2E"
)

// unwrapE2EMessage recursively unwraps nested protobuf message wrappers
// (DeviceSentMessage, EphemeralMessage, ViewOnceMessage, ViewOnceMessageV2,
// ViewOnceMessageV2Extension, DocumentWithCaptionMessage, BotInvokeMessage,
// and EditedMessage) to extract the underlying message payload.
// It also returns whether any View-Once wrapper was detected.
func unwrapE2EMessage(msg *waE2E.Message) (*waE2E.Message, bool) {
	if msg == nil {
		return nil, false
	}

	current := msg
	isViewOnce := false

	for {
		if dsm := current.GetDeviceSentMessage(); dsm != nil && dsm.GetMessage() != nil {
			current = dsm.GetMessage()
			continue
		}
		if eph := current.GetEphemeralMessage(); eph != nil && eph.GetMessage() != nil {
			current = eph.GetMessage()
			continue
		}
		if vo := current.GetViewOnceMessage(); vo != nil && vo.GetMessage() != nil {
			current = vo.GetMessage()
			isViewOnce = true
			continue
		}
		if vo2 := current.GetViewOnceMessageV2(); vo2 != nil && vo2.GetMessage() != nil {
			current = vo2.GetMessage()
			isViewOnce = true
			continue
		}
		if vo2ext := current.GetViewOnceMessageV2Extension(); vo2ext != nil && vo2ext.GetMessage() != nil {
			current = vo2ext.GetMessage()
			isViewOnce = true
			continue
		}
		if doc := current.GetDocumentWithCaptionMessage(); doc != nil && doc.GetMessage() != nil {
			current = doc.GetMessage()
			continue
		}
		if bot := current.GetBotInvokeMessage(); bot != nil && bot.GetMessage() != nil {
			current = bot.GetMessage()
			continue
		}
		if edit := current.GetEditedMessage(); edit != nil && edit.GetMessage() != nil {
			current = edit.GetMessage()
			continue
		}
		break
	}

	return current, isViewOnce
}
