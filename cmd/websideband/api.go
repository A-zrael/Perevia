package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	appstore "github.com/azrael/rnode-chat/internal/store"
)

type outboundMessage struct {
	Destination string        `json:"destination"`
	Content     string        `json:"content"`
	Title       string        `json:"title"`
	Method      string        `json:"method"`
	Image       *encodedMedia `json:"image"`
	Audio       *encodedMedia `json:"audio"`
}

func (app *application) sendMessage(writer http.ResponseWriter, request *http.Request) {
	payload, ok := readBoundedBody(writer, request, maxRequestBody)
	if !ok {
		return
	}
	var outbound outboundMessage
	if err := json.Unmarshal(payload, &outbound); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "request body must be valid JSON"})
		return
	}
	response, err := app.bridgeRequest(request.Context(), http.MethodPost, "/v1/messages", bytes.NewReader(payload))
	if err != nil {
		app.log.Warn("bridge request failed", "path", "/v1/messages", "error", err)
		writeJSON(writer, http.StatusBadGateway, map[string]string{"error": "LXMF bridge unavailable"})
		return
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxRequestBody))
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]string{"error": "invalid LXMF bridge response"})
		return
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		var result struct {
			RequestID string `json:"request_id"`
			MessageID string `json:"message_id"`
			State     string `json:"state"`
		}
		if err := json.Unmarshal(responseBody, &result); err == nil {
			message := appstore.Message{
				ID: result.MessageID, LXMFID: result.MessageID, RequestID: result.RequestID,
				Peer: outbound.Destination, Direction: "outgoing", Content: outbound.Content,
				Timestamp: time.Now().UnixMilli(), State: result.State,
			}
			if message.ID == "" {
				message.ID = result.RequestID
			}
			if message.ID == "" {
				message.ID = randomID()
			}
			if err := app.persistOutboundMedia(&message, outbound); err != nil {
				app.log.Warn("outbound media persistence failed", "error", err)
			}
			if err := app.store.UpsertMessage(request.Context(), message); err != nil {
				app.log.Warn("outbound message persistence failed", "error", err)
			}
		}
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(response.StatusCode)
	_, _ = writer.Write(responseBody)
}

func (app *application) persistOutboundMedia(message *appstore.Message, outbound outboundMessage) error {
	if outbound.Image != nil && outbound.Image.Format == "webp" {
		data, err := base64.StdEncoding.DecodeString(outbound.Image.Data)
		if err != nil {
			return err
		}
		path, err := app.store.SaveAttachment(data, "webp")
		if err != nil {
			return err
		}
		message.ImagePath, message.ImageMIME = path, "image/webp"
	}
	if outbound.Audio != nil && outbound.Audio.Mode == "opus_ogg" {
		data, err := base64.StdEncoding.DecodeString(outbound.Audio.Data)
		if err != nil {
			return err
		}
		path, err := app.store.SaveAttachment(data, "ogg")
		if err != nil {
			return err
		}
		message.AudioPath, message.AudioMIME = path, "audio/ogg"
	}
	return nil
}

func (app *application) listMessages(writer http.ResponseWriter, request *http.Request) {
	messages, err := app.store.ListMessages(request.Context(), 500)
	if err != nil {
		app.log.Error("message listing failed", "error", err)
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "messages could not be loaded"})
		return
	}
	for index := range messages {
		if messages[index].ImagePath != "" {
			messages[index].ImageURL = "/api/v1/messages/" + messages[index].ID + "/image"
		}
		if messages[index].AudioPath != "" {
			messages[index].AudioURL = "/api/v1/messages/" + messages[index].ID + "/audio"
		}
	}
	states, err := app.store.ConversationStates(request.Context())
	if err != nil {
		app.log.Error("conversation state listing failed", "error", err)
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "conversation state could not be loaded"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"messages": messages, "conversations": states})
}

func (app *application) messageAttachment(writer http.ResponseWriter, request *http.Request) {
	kind := request.PathValue("kind")
	if kind != "image" && kind != "audio" {
		http.NotFound(writer, request)
		return
	}
	path, mime, err := app.store.Attachment(request.Context(), request.PathValue("id"), kind)
	if err != nil || path == "" {
		http.NotFound(writer, request)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Content-Type", mime)
	writer.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	writer.Header().Set("Content-Disposition", "inline")
	http.ServeContent(writer, request, info.Name(), info.ModTime(), file)
}

func (app *application) deleteConversation(writer http.ResponseWriter, request *http.Request) {
	address := strings.ToLower(request.PathValue("address"))
	if !validAddress(address) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid LXMF address"})
		return
	}
	deleted, err := app.store.DeleteConversation(request.Context(), address)
	if err != nil {
		app.log.Error("conversation deletion failed", "peer", address, "error", err)
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "conversation could not be deleted"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"deleted": deleted})
}

func (app *application) markConversationRead(writer http.ResponseWriter, request *http.Request) {
	address := strings.ToLower(request.PathValue("address"))
	if !validAddress(address) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid LXMF address"})
		return
	}
	lastReadAt, err := app.store.MarkConversationRead(request.Context(), address)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "conversation could not be marked read"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"peer": address, "last_read_at": lastReadAt, "unread": 0})
}

func (app *application) listContacts(writer http.ResponseWriter, request *http.Request) {
	contacts, err := app.store.ListContacts(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "contacts could not be loaded"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"contacts": contacts})
}

func (app *application) saveContact(writer http.ResponseWriter, request *http.Request) {
	payload, ok := readBoundedBody(writer, request, 16*1024)
	if !ok {
		return
	}
	var contact appstore.Contact
	if err := json.Unmarshal(payload, &contact); err != nil || !validAddress(contact.Address) || strings.TrimSpace(contact.Name) == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "valid contact name and LXMF address are required"})
		return
	}
	contact.Address = strings.ToLower(contact.Address)
	contact.Name = strings.TrimSpace(contact.Name)
	if err := app.store.UpsertContact(request.Context(), contact); err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "contact could not be saved"})
		return
	}
	writeJSON(writer, http.StatusOK, contact)
}

func (app *application) removeContact(writer http.ResponseWriter, request *http.Request) {
	address := strings.ToLower(request.PathValue("address"))
	if !validAddress(address) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid LXMF address"})
		return
	}
	if err := app.store.DeleteContact(request.Context(), address); err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "contact could not be deleted"})
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func readBoundedBody(writer http.ResponseWriter, request *http.Request, limit int64) ([]byte, bool) {
	request.Body = http.MaxBytesReader(writer, request.Body, limit)
	payload, err := io.ReadAll(request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return nil, false
		}
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return nil, false
	}
	return payload, true
}

func validAddress(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func randomID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buffer)
}
