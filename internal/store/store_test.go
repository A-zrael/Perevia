package store

import (
	"context"
	"os"
	"testing"
)

func TestMessagesContactsAndAttachmentsPersist(t *testing.T) {
	dataDir := t.TempDir()
	persistence, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	contact := Contact{Address: "0123456789abcdef0123456789abcdef", Name: "Alice"}
	if err := persistence.UpsertContact(ctx, contact); err != nil {
		t.Fatal(err)
	}
	imagePath, err := persistence.SaveAttachment([]byte("webp-data"), "webp")
	if err != nil {
		t.Fatal(err)
	}
	message := Message{ID: "message-one", Peer: contact.Address, Direction: "incoming", Content: "Photo", Timestamp: 1234, State: "delivered", ImagePath: imagePath, ImageMIME: "image/webp"}
	if err := persistence.UpsertMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	contacts, err := reopened.ListContacts(ctx)
	if err != nil || len(contacts) != 1 || contacts[0].Name != "Alice" {
		t.Fatalf("contacts=%v err=%v", contacts, err)
	}
	messages, err := reopened.ListMessages(ctx, 10)
	if err != nil || len(messages) != 1 || messages[0].ImageMIME != "image/webp" {
		t.Fatalf("messages=%v err=%v", messages, err)
	}
	path, mime, err := reopened.Attachment(ctx, "message-one", "image")
	if err != nil || mime != "image/webp" {
		t.Fatalf("path=%q mime=%q err=%v", path, mime, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "webp-data" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestDeleteConversationKeepsContactAndRemovesOrphanAttachment(t *testing.T) {
	persistence, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close()
	ctx := context.Background()
	const address = "0123456789abcdef0123456789abcdef"
	if err := persistence.UpsertContact(ctx, Contact{Address: address, Name: "Alice"}); err != nil {
		t.Fatal(err)
	}
	attachmentPath, err := persistence.SaveAttachment([]byte("voice-data"), "ogg")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistence.UpsertMessage(ctx, Message{ID: "message-one", Peer: address, Direction: "outgoing", Content: "Voice", Timestamp: 1234, State: "delivered", AudioPath: attachmentPath, AudioMIME: "audio/ogg"}); err != nil {
		t.Fatal(err)
	}
	deleted, err := persistence.DeleteConversation(ctx, address)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	messages, err := persistence.ListMessages(ctx, 10)
	if err != nil || len(messages) != 0 {
		t.Fatalf("messages=%v err=%v", messages, err)
	}
	contacts, err := persistence.ListContacts(ctx)
	if err != nil || len(contacts) != 1 {
		t.Fatalf("contacts=%v err=%v", contacts, err)
	}
	if _, err := os.Stat(attachmentPath); !os.IsNotExist(err) {
		t.Fatalf("attachment still exists: %v", err)
	}
}

func TestConversationUnreadStatePersists(t *testing.T) {
	dataDir := t.TempDir()
	persistence, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const address = "0123456789abcdef0123456789abcdef"
	for _, message := range []Message{
		{ID: "incoming-one", Peer: address, Direction: "incoming", Content: "First", Timestamp: 1000, State: "delivered"},
		{ID: "outgoing-one", Peer: address, Direction: "outgoing", Content: "Reply", Timestamp: 2000, State: "sent"},
		{ID: "incoming-two", Peer: address, Direction: "incoming", Content: "Second", Timestamp: 3000, State: "delivered"},
	} {
		if err := persistence.UpsertMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	states, err := persistence.ConversationStates(ctx)
	if err != nil || len(states) != 1 || states[0].Unread != 2 {
		t.Fatalf("states=%v err=%v", states, err)
	}
	if _, err := persistence.MarkConversationRead(ctx, address); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	states, err = reopened.ConversationStates(ctx)
	if err != nil || len(states) != 1 || states[0].Unread != 0 || states[0].LastReadAt != 3000 {
		t.Fatalf("states=%v err=%v", states, err)
	}
}
