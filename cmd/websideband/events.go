package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	appstore "github.com/azrael/rnode-chat/internal/store"
)

type realtimeEvent struct {
	name string
	data []byte
}

type eventHub struct {
	mu          sync.RWMutex
	subscribers map[chan realtimeEvent]struct{}
}

type bridgeEnvelope struct {
	Type      string          `json:"type"`
	Timestamp float64         `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

type encodedMedia struct {
	Format string `json:"format"`
	Mode   string `json:"mode"`
	Data   string `json:"data"`
}

func newEventHub() *eventHub {
	return &eventHub{subscribers: make(map[chan realtimeEvent]struct{})}
}

func (hub *eventHub) subscribe() chan realtimeEvent {
	subscriber := make(chan realtimeEvent, 64)
	hub.mu.Lock()
	hub.subscribers[subscriber] = struct{}{}
	hub.mu.Unlock()
	return subscriber
}

func (hub *eventHub) unsubscribe(subscriber chan realtimeEvent) {
	hub.mu.Lock()
	delete(hub.subscribers, subscriber)
	close(subscriber)
	hub.mu.Unlock()
}

func (hub *eventHub) publish(event realtimeEvent) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for subscriber := range hub.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (app *application) runBridgeEvents(ctx context.Context) {
	for ctx.Err() == nil {
		if err := app.consumeBridgeEvents(ctx); err != nil && ctx.Err() == nil {
			app.log.Warn("bridge event stream disconnected", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (app *application) consumeBridgeEvents(ctx context.Context) error {
	response, err := app.bridgeRequestWithClient(ctx, app.stream, http.MethodGet, "/v1/events", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxRequestBody))
		return fmt.Errorf("bridge events returned %s", response.Status)
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	eventName := "message"
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			data := []byte(strings.TrimPrefix(line, "data: "))
			if err := app.persistBridgeEvent(ctx, data); err != nil {
				app.log.Warn("bridge event persistence failed", "event", eventName, "error", err)
			}
			app.eventHub.publish(realtimeEvent{name: eventName, data: append([]byte(nil), data...)})
		}
	}
	return scanner.Err()
}

func (app *application) persistBridgeEvent(ctx context.Context, raw []byte) error {
	var envelope bridgeEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	switch envelope.Type {
	case "message_received":
		var incoming struct {
			MessageID string        `json:"message_id"`
			Source    string        `json:"source"`
			Content   string        `json:"content"`
			Timestamp float64       `json:"timestamp"`
			Image     *encodedMedia `json:"image"`
			Audio     *encodedMedia `json:"audio"`
		}
		if err := json.Unmarshal(envelope.Data, &incoming); err != nil {
			return err
		}
		message := appstore.Message{
			ID: incoming.MessageID, LXMFID: incoming.MessageID, Peer: incoming.Source, Direction: "incoming",
			Content: incoming.Content, Timestamp: int64(incoming.Timestamp * 1000), State: "delivered",
		}
		if message.ID == "" {
			message.ID = randomID()
		}
		if incoming.Image != nil {
			data, err := base64.StdEncoding.DecodeString(incoming.Image.Data)
			if err != nil {
				return err
			}
			format := strings.ToLower(strings.TrimPrefix(incoming.Image.Format, "."))
			mime := map[string]string{"webp": "image/webp", "png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg"}[format]
			if mime != "" {
				path, err := app.store.SaveAttachment(data, format)
				if err != nil {
					return err
				}
				message.ImagePath, message.ImageMIME = path, mime
			}
		}
		if incoming.Audio != nil && incoming.Audio.Mode == "opus_ogg" {
			data, err := base64.StdEncoding.DecodeString(incoming.Audio.Data)
			if err != nil {
				return err
			}
			path, err := app.store.SaveAttachment(data, "ogg")
			if err != nil {
				return err
			}
			message.AudioPath, message.AudioMIME = path, "audio/ogg"
		}
		return app.store.UpsertMessage(ctx, message)
	case "message_status":
		var update struct {
			RequestID string `json:"request_id"`
			MessageID string `json:"message_id"`
			State     string `json:"state"`
		}
		if err := json.Unmarshal(envelope.Data, &update); err != nil {
			return err
		}
		return app.store.UpdateMessageState(ctx, update.RequestID, update.MessageID, update.State)
	}
	return nil
}
