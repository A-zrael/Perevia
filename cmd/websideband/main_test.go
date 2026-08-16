package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	appstore "github.com/azrael/rnode-chat/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func newTestApplication(t *testing.T, transport http.RoundTripper) *application {
	t.Helper()
	client := &http.Client{Transport: transport}
	persistence, err := appstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auth, err := newAuthManager(persistence, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistence.Close() })
	return &application{
		bridgeURL: "http://bridge.test",
		client:    client,
		stream:    client,
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		transcode: func(_ context.Context, input io.Reader, output io.Writer) error {
			_, err := io.Copy(output, input)
			return err
		},
		prepareImage: func(_ context.Context, input io.Reader, output io.Writer) error {
			_, err := io.Copy(output, input)
			return err
		},
		store:    persistence,
		eventHub: newEventHub(),
		auth:     auth,
	}
}

func TestAuthenticationSetupLoginAndProtection(t *testing.T) {
	app := newTestApplication(t, roundTripFunc(func(_ *http.Request) (*http.Response, error) { return bridgeResponse(http.StatusOK, `{}`), nil }))
	app.auth.disabled = false

	protected := httptest.NewRecorder()
	app.routes().ServeHTTP(protected, httptest.NewRequest(http.MethodGet, "/api/v1/messages", nil))
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("protected status=%d", protected.Code)
	}

	setup := httptest.NewRecorder()
	setupRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", strings.NewReader(`{"username":"admin","password":"correct horse battery staple"}`))
	app.routes().ServeHTTP(setup, setupRequest)
	if setup.Code != http.StatusCreated || len(setup.Result().Cookies()) != 1 {
		t.Fatalf("setup status=%d body=%q cookies=%v", setup.Code, setup.Body.String(), setup.Result().Cookies())
	}

	authenticated := httptest.NewRecorder()
	authenticatedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/messages", nil)
	authenticatedRequest.AddCookie(setup.Result().Cookies()[0])
	app.routes().ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusOK {
		t.Fatalf("authenticated status=%d body=%q", authenticated.Code, authenticated.Body.String())
	}
}

func TestImagePrepare(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/images/prepare", strings.NewReader("picture"))
	request.Header.Set("Content-Type", "image/jpeg")
	newTestApplication(t, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		t.Fatal("image route must not call bridge")
		return nil, nil
	})).routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "picture" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("unexpected content type %q", response.Header().Get("Content-Type"))
	}
}

func TestAudioTranscode(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/audio/transcode", strings.NewReader("recording"))
	request.Header.Set("Content-Type", "audio/webm")
	newTestApplication(t, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		t.Fatal("audio route must not call bridge")
		return nil, nil
	})).routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "recording" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "audio/ogg" {
		t.Fatalf("unexpected content type %q", response.Header().Get("Content-Type"))
	}
}

func TestStatusProxy(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/status" {
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
		return bridgeResponse(http.StatusOK, `{"address":"001122"}`), nil
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	newTestApplication(t, transport).routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "001122") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestWebInterfaceIsEmbedded(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	newTestApplication(t, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		t.Fatal("static route must not call bridge")
		return nil, nil
	})).routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "PEREVIA") || !strings.Contains(response.Body.String(), "RNS RPC key") || !strings.Contains(response.Body.String(), "RNSD machine / bridge IP") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("unexpected content type %q", contentType)
	}
	if policy := response.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "connect-src 'self'") {
		t.Fatalf("unexpected content security policy %q", policy)
	}
}

func TestManifestIsEmbedded(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil)
	newTestApplication(t, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		t.Fatal("static route must not call bridge")
		return nil, nil
	})).routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"display": "standalone"`) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestAddressQRRoundTrip(t *testing.T) {
	const address = "0123456789abcdef0123456789abcdef"
	app := newTestApplication(t, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		t.Fatal("QR routes must not call bridge")
		return nil, nil
	}))

	generated := httptest.NewRecorder()
	app.routes().ServeHTTP(generated, httptest.NewRequest(http.MethodGet, "/api/v1/qr?address="+url.QueryEscape(address), nil))
	if generated.Code != http.StatusOK || generated.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("generate status=%d type=%q", generated.Code, generated.Header().Get("Content-Type"))
	}

	decoded := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/qr/decode", bytes.NewReader(generated.Body.Bytes()))
	request.Header.Set("Content-Type", "image/png")
	app.routes().ServeHTTP(decoded, request)
	if decoded.Code != http.StatusOK || !strings.Contains(decoded.Body.String(), address) {
		t.Fatalf("decode status=%d body=%q", decoded.Code, decoded.Body.String())
	}
}

func TestExtractAddressSupportsCommonWrappers(t *testing.T) {
	const address = "0123456789abcdef0123456789abcdef"
	for _, value := range []string{address, "lxmf://" + address, "LXMF:" + strings.ToUpper(address), "contact " + address} {
		if got := extractAddress(value); got != address {
			t.Errorf("extractAddress(%q)=%q", value, got)
		}
	}
}

func TestMessageProxyForwardsTokenAndBody(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("missing bridge bearer token")
		}
		body, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(body), "hello") {
			t.Fatalf("unexpected body %q", body)
		}
		if request.ContentLength != int64(len(body)) {
			t.Fatalf("content length=%d want=%d", request.ContentLength, len(body))
		}
		if len(request.TransferEncoding) != 0 {
			t.Fatalf("unexpected transfer encoding %v", request.TransferEncoding)
		}
		return bridgeResponse(http.StatusAccepted, `{"state":"queued"}`), nil
	})

	app := newTestApplication(t, transport)
	app.bridgeToken = "secret"
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/messages", strings.NewReader(`{"content":"hello"}`))
	app.routes().ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestDisplayNameUpdateProxiesToBridge(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPut || request.URL.Path != "/v1/settings/identity" {
			t.Fatalf("unexpected bridge request %s %s", request.Method, request.URL.Path)
		}
		body, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(body), "Radio House") {
			t.Fatalf("unexpected body %q", body)
		}
		return bridgeResponse(http.StatusOK, `{"display_name":"Radio House"}`), nil
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings/identity", strings.NewReader(`{"display_name":"Radio House"}`))
	newTestApplication(t, transport).routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Radio House") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestDeleteConversation(t *testing.T) {
	app := newTestApplication(t, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		t.Fatal("conversation deletion must not call bridge")
		return nil, nil
	}))
	const address = "0123456789abcdef0123456789abcdef"
	if err := app.store.UpsertMessage(context.Background(), appstore.Message{ID: "message-one", Peer: address, Direction: "incoming", Content: "Hello", Timestamp: 1234, State: "delivered"}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/conversations/"+address, nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"deleted":1`) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestMarkConversationRead(t *testing.T) {
	app := newTestApplication(t, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		t.Fatal("read tracking must not call bridge")
		return nil, nil
	}))
	const address = "0123456789abcdef0123456789abcdef"
	if err := app.store.UpsertMessage(context.Background(), appstore.Message{ID: "message-one", Peer: address, Direction: "incoming", Content: "Hello", Timestamp: 1234, State: "delivered"}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.routes().ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/v1/conversations/"+address+"/read", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"last_read_at":1234`) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestEventStreamProxy(t *testing.T) {
	app := newTestApplication(t, roundTripFunc(func(_ *http.Request) (*http.Response, error) { return nil, errors.New("unused") }))
	recorder := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { app.routes().ServeHTTP(recorder, request); close(done) }()
	time.Sleep(10 * time.Millisecond)
	app.eventHub.publish(realtimeEvent{name: "ready", data: []byte(`{}`)})
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done
	if !strings.Contains(recorder.Body.String(), "event: ready") {
		t.Fatalf("unexpected event stream %q", recorder.Body.String())
	}
}

func bridgeResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}
