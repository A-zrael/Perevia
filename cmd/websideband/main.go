package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	appstore "github.com/azrael/rnode-chat/internal/store"
)

const (
	maxRequestBody = 2 << 20
	maxAudioInput  = 8 << 20
	maxAudioOutput = 1 << 20
	maxImageInput  = 12 << 20
	maxImageOutput = 1 << 20
)

//go:embed web/*
var webAssets embed.FS

type config struct {
	listenAddress string
	bridgeURL     string
	bridgeToken   string
	dataDir       string
	tlsCertFile   string
	tlsKeyFile    string
	authDisabled  bool
}

type application struct {
	bridgeURL    string
	bridgeToken  string
	client       *http.Client
	stream       *http.Client
	log          *slog.Logger
	transcode    func(context.Context, io.Reader, io.Writer) error
	prepareImage func(context.Context, io.Reader, io.Writer) error
	store        *appstore.Store
	eventHub     *eventHub
	auth         *authManager
}

func main() {
	cfg := loadConfig()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	persistence, err := appstore.Open(cfg.dataDir)
	if err != nil {
		log.Error("persistence initialization failed", "error", err)
		os.Exit(1)
	}
	defer persistence.Close()
	auth, err := newAuthManager(persistence, cfg.authDisabled)
	if err != nil {
		log.Error("authentication initialization failed", "error", err)
		os.Exit(1)
	}
	app := &application{
		bridgeURL:    strings.TrimRight(cfg.bridgeURL, "/"),
		bridgeToken:  cfg.bridgeToken,
		client:       &http.Client{Timeout: 15 * time.Second},
		stream:       &http.Client{},
		log:          log,
		transcode:    transcodeAudio,
		prepareImage: prepareImage,
		store:        persistence,
		eventHub:     newEventHub(),
		auth:         auth,
	}

	server := &http.Server{
		Addr:              cfg.listenAddress,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go app.runBridgeEvents(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Error("HTTP shutdown failed", "error", err)
		}
	}()

	tlsEnabled := cfg.tlsCertFile != "" && cfg.tlsKeyFile != ""
	log.Info("PEREVIA listening", "address", cfg.listenAddress, "tls", tlsEnabled, "authentication", !cfg.authDisabled)
	if (cfg.tlsCertFile == "") != (cfg.tlsKeyFile == "") {
		log.Error("both WEBSIDEBAND_TLS_CERT_FILE and WEBSIDEBAND_TLS_KEY_FILE are required")
		os.Exit(1)
	}
	var serveError error
	if tlsEnabled {
		serveError = server.ListenAndServeTLS(cfg.tlsCertFile, cfg.tlsKeyFile)
	} else {
		serveError = server.ListenAndServe()
	}
	if serveError != nil && !errors.Is(serveError, http.ErrServerClosed) {
		log.Error("HTTP server failed", "error", serveError)
		os.Exit(1)
	}
}

func loadConfig() config {
	return config{
		listenAddress: envOrDefault("WEBSIDEBAND_LISTEN_ADDRESS", ":8080"),
		bridgeURL:     envOrDefault("LXMF_BRIDGE_URL", "http://reticulum:8081"),
		bridgeToken:   strings.TrimSpace(os.Getenv("LXMF_BRIDGE_TOKEN")),
		dataDir:       envOrDefault("WEBSIDEBAND_DATA_DIR", "./data"),
		tlsCertFile:   strings.TrimSpace(os.Getenv("WEBSIDEBAND_TLS_CERT_FILE")),
		tlsKeyFile:    strings.TrimSpace(os.Getenv("WEBSIDEBAND_TLS_KEY_FILE")),
		authDisabled:  envBool("WEBSIDEBAND_AUTH_DISABLED", false),
	}
}

func envBool(name string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()
	staticAssets, err := fs.Sub(webAssets, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(staticAssets)))
	mux.HandleFunc("GET /healthz", app.health)
	mux.HandleFunc("GET /api/v1/auth/status", app.auth.status)
	mux.HandleFunc("POST /api/v1/auth/setup", app.auth.setup)
	mux.HandleFunc("POST /api/v1/auth/login", app.auth.login)
	mux.HandleFunc("POST /api/v1/auth/logout", app.auth.logout)
	mux.HandleFunc("GET /api/v1/status", app.proxyJSON("GET", "/v1/status"))
	mux.HandleFunc("GET /api/v1/messages", app.listMessages)
	mux.HandleFunc("POST /api/v1/messages", app.sendMessage)
	mux.HandleFunc("GET /api/v1/messages/{id}/{kind}", app.messageAttachment)
	mux.HandleFunc("DELETE /api/v1/conversations/{address}", app.deleteConversation)
	mux.HandleFunc("PUT /api/v1/conversations/{address}/read", app.markConversationRead)
	mux.HandleFunc("GET /api/v1/contacts", app.listContacts)
	mux.HandleFunc("POST /api/v1/contacts", app.saveContact)
	mux.HandleFunc("DELETE /api/v1/contacts/{address}", app.removeContact)
	mux.HandleFunc("GET /api/v1/qr", app.generateAddressQR)
	mux.HandleFunc("POST /api/v1/qr/decode", app.decodeAddressQR)
	mux.HandleFunc("POST /api/v1/audio/transcode", app.audioTranscode)
	mux.HandleFunc("POST /api/v1/images/prepare", app.imagePrepare)
	mux.HandleFunc("POST /api/v1/announce", app.proxyJSON("POST", "/v1/announce"))
	mux.HandleFunc("PUT /api/v1/settings/identity", app.proxyJSON("PUT", "/v1/settings/identity"))
	mux.HandleFunc("GET /api/v1/events", app.events)
	return requestLogger(app.log, securityHeaders(app.auth.middleware(mux)))
}

func (app *application) imagePrepare(writer http.ResponseWriter, request *http.Request) {
	if !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "image/") {
		writeJSON(writer, http.StatusUnsupportedMediaType, map[string]string{"error": "an image content type is required"})
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxImageInput)
	input, err := io.ReadAll(request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]string{"error": "image is too large"})
			return
		}
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "could not read image"})
		return
	}
	if len(input) == 0 {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "image is empty"})
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := app.prepareImage(ctx, bytes.NewReader(input), &output); err != nil {
		app.log.Warn("image preparation failed", "error", err)
		writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"error": "image format could not be converted"})
		return
	}
	if output.Len() == 0 || output.Len() > maxImageOutput {
		writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"error": "prepared image has an invalid size"})
		return
	}
	writer.Header().Set("Content-Type", "image/webp")
	writer.Header().Set("Content-Length", fmt.Sprint(output.Len()))
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	_, _ = output.WriteTo(writer)
}

func prepareImage(ctx context.Context, input io.Reader, output io.Writer) error {
	command := exec.CommandContext(ctx, "ffmpeg",
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", "pipe:0", "-map_metadata", "-1", "-an", "-frames:v", "1",
		"-vf", "scale=w='min(1024,iw)':h='min(1024,ih)':force_original_aspect_ratio=decrease",
		"-c:v", "libwebp", "-quality", "55", "-compression_level", "6", "-f", "image2pipe", "pipe:1",
	)
	command.Stdin = input
	command.Stdout = output
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (app *application) audioTranscode(writer http.ResponseWriter, request *http.Request) {
	if !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "audio/") {
		writeJSON(writer, http.StatusUnsupportedMediaType, map[string]string{"error": "an audio content type is required"})
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxAudioInput)
	input, err := io.ReadAll(request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]string{"error": "audio recording is too large"})
			return
		}
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "could not read audio recording"})
		return
	}
	if len(input) == 0 {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "audio recording is empty"})
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := app.transcode(ctx, bytes.NewReader(input), &output); err != nil {
		app.log.Warn("audio transcoding failed", "error", err)
		writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"error": "audio format could not be converted"})
		return
	}
	if output.Len() == 0 || output.Len() > maxAudioOutput {
		writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"error": "converted voice note has an invalid size"})
		return
	}
	writer.Header().Set("Content-Type", "audio/ogg")
	writer.Header().Set("Content-Length", fmt.Sprint(output.Len()))
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	_, _ = output.WriteTo(writer)
}

func transcodeAudio(ctx context.Context, input io.Reader, output io.Writer) error {
	command := exec.CommandContext(ctx, "ffmpeg",
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", "pipe:0", "-map_metadata", "-1", "-vn", "-t", "60",
		"-ac", "1", "-ar", "12000", "-c:a", "libopus", "-application", "voip",
		"-b:a", "12k", "-vbr", "on", "-compression_level", "10", "-f", "ogg", "pipe:1",
	)
	command.Stdin = input
	command.Stdout = output
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (app *application) health(writer http.ResponseWriter, request *http.Request) {
	response, err := app.bridgeRequest(request.Context(), http.MethodGet, "/healthz", nil)
	if err != nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (app *application) proxyJSON(method, path string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		var body io.Reader
		if method == http.MethodPost || method == http.MethodPut {
			request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBody)
			payload, err := io.ReadAll(request.Body)
			if err != nil {
				var maxBytesError *http.MaxBytesError
				if errors.As(err, &maxBytesError) {
					writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
					return
				}
				writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			body = bytes.NewReader(payload)
		}
		response, err := app.bridgeRequest(request.Context(), method, path, body)
		if err != nil {
			app.log.Warn("bridge request failed", "path", path, "error", err)
			writeJSON(writer, http.StatusBadGateway, map[string]string{"error": "LXMF bridge unavailable"})
			return
		}
		defer response.Body.Close()
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(response.StatusCode)
		if _, err := io.Copy(writer, io.LimitReader(response.Body, maxRequestBody)); err != nil {
			app.log.Warn("bridge response copy failed", "path", path, "error", err)
		}
	}
}

func (app *application) events(writer http.ResponseWriter, request *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()
	subscriber := app.eventHub.subscribe()
	defer app.eventHub.unsubscribe(subscriber)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event := <-subscriber:
			if _, err := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event.name, event.data); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := io.WriteString(writer, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (app *application) bridgeRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	return app.bridgeRequestWithClient(ctx, app.client, method, path, body)
}

func (app *application) bridgeRequestWithClient(ctx context.Context, client *http.Client, method, path string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, app.bridgeURL+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if app.bridgeToken != "" {
		request.Header.Set("Authorization", "Bearer "+app.bridgeToken)
	}
	return client.Do(request)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; media-src 'self' data: blob:; style-src 'self'; script-src 'self'; manifest-src 'self'; worker-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if request.TLS != nil {
			writer.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(writer, request)
	})
}

func requestLogger(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(writer, request)
		log.Info("HTTP request", "method", request.Method, "path", request.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "encode JSON response:", err)
	}
}
