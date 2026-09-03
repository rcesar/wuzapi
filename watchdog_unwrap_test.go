package main

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestUnwrapE2EMessage(t *testing.T) {
	t.Run("nil message", func(t *testing.T) {
		unwrapped, isVO := unwrapE2EMessage(nil)
		if unwrapped != nil || isVO {
			t.Errorf("expected nil, false; got %v, %v", unwrapped, isVO)
		}
	})

	t.Run("direct conversation message", func(t *testing.T) {
		msg := &waE2E.Message{
			Conversation: proto.String("hello world"),
		}
		unwrapped, isVO := unwrapE2EMessage(msg)
		if unwrapped == nil || unwrapped.GetConversation() != "hello world" || isVO {
			t.Errorf("expected conversation 'hello world', isVO=false; got %v, %v", unwrapped, isVO)
		}
	})

	t.Run("view once image message", func(t *testing.T) {
		inner := &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				Caption: proto.String("test caption"),
			},
		}
		msg := &waE2E.Message{
			ViewOnceMessage: &waE2E.FutureProofMessage{
				Message: inner,
			},
		}
		unwrapped, isVO := unwrapE2EMessage(msg)
		if unwrapped == nil || unwrapped.GetImageMessage() == nil || unwrapped.GetImageMessage().GetCaption() != "test caption" || !isVO {
			t.Errorf("expected unwrapped image with caption, isVO=true; got %v, %v", unwrapped, isVO)
		}
	})

	t.Run("nested device sent, ephemeral, and view once v2", func(t *testing.T) {
		inner := &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				Caption: proto.String("deeply nested image"),
			},
		}
		vo2 := &waE2E.Message{
			ViewOnceMessageV2: &waE2E.FutureProofMessage{
				Message: inner,
			},
		}
		eph := &waE2E.Message{
			EphemeralMessage: &waE2E.FutureProofMessage{
				Message: vo2,
			},
		}
		dsm := &waE2E.Message{
			DeviceSentMessage: &waE2E.DeviceSentMessage{
				Message: eph,
			},
		}

		unwrapped, isVO := unwrapE2EMessage(dsm)
		if unwrapped == nil || unwrapped.GetImageMessage() == nil || unwrapped.GetImageMessage().GetCaption() != "deeply nested image" || !isVO {
			t.Errorf("expected unwrapped image from nested wrappers, isVO=true; got %v, %v", unwrapped, isVO)
		}
	})
}
