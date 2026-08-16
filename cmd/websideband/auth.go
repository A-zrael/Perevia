package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	appstore "github.com/azrael/rnode-chat/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const (
	authUsernameKey = "auth.username"
	authPasswordKey = "auth.password_bcrypt"
	sessionCookie   = "websideband_session"
	sessionLifetime = 7 * 24 * time.Hour
)

type authManager struct {
	store      *appstore.Store
	disabled   bool
	configured bool
	username   string
	password   []byte
	sessions   map[string]time.Time
	mutex      sync.RWMutex
	sessionMux sync.RWMutex
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func newAuthManager(store *appstore.Store, disabled bool) (*authManager, error) {
	manager := &authManager{store: store, disabled: disabled, sessions: make(map[string]time.Time)}
	username, usernameErr := store.Setting(context.Background(), authUsernameKey)
	password, passwordErr := store.Setting(context.Background(), authPasswordKey)
	if usernameErr == nil && passwordErr == nil {
		manager.configured, manager.username, manager.password = true, username, []byte(password)
		return manager, nil
	}
	if (!errors.Is(usernameErr, sql.ErrNoRows) && usernameErr != nil) || (!errors.Is(passwordErr, sql.ErrNoRows) && passwordErr != nil) {
		return nil, errors.New("load authentication settings")
	}
	return manager, nil
}

func (manager *authManager) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if manager.disabled || request.URL.Path == "/healthz" || strings.HasPrefix(request.URL.Path, "/api/v1/auth/") || !strings.HasPrefix(request.URL.Path, "/api/") {
			next.ServeHTTP(writer, request)
			return
		}
		if !manager.authenticated(request) {
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead && !sameOrigin(request) {
			writeJSON(writer, http.StatusForbidden, map[string]string{"error": "cross-origin request rejected"})
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (manager *authManager) status(writer http.ResponseWriter, request *http.Request) {
	manager.mutex.RLock()
	configured, username := manager.configured, manager.username
	manager.mutex.RUnlock()
	writeJSON(writer, http.StatusOK, map[string]any{"configured": configured, "authenticated": manager.disabled || manager.authenticated(request), "username": username, "disabled": manager.disabled, "secure": request.TLS != nil})
}

func (manager *authManager) setup(writer http.ResponseWriter, request *http.Request) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if manager.configured {
		writeJSON(writer, http.StatusConflict, map[string]string{"error": "authentication is already configured"})
		return
	}
	credentials, ok := readCredentials(writer, request)
	if !ok {
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(credentials.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "password could not be secured"})
		return
	}
	if err := manager.store.SetSetting(request.Context(), authUsernameKey, credentials.Username); err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "login could not be saved"})
		return
	}
	if err := manager.store.SetSetting(request.Context(), authPasswordKey, string(hash)); err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "login could not be saved"})
		return
	}
	manager.configured, manager.username, manager.password = true, credentials.Username, hash
	manager.issueSession(writer, request)
	writeJSON(writer, http.StatusCreated, map[string]any{"authenticated": true, "username": credentials.Username})
}

func (manager *authManager) login(writer http.ResponseWriter, request *http.Request) {
	credentials, ok := readCredentials(writer, request)
	if !ok {
		return
	}
	manager.mutex.RLock()
	configured, username, password := manager.configured, manager.username, append([]byte(nil), manager.password...)
	manager.mutex.RUnlock()
	if !configured || credentials.Username != username || bcrypt.CompareHashAndPassword(password, []byte(credentials.Password)) != nil {
		time.Sleep(350 * time.Millisecond)
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "incorrect username or password"})
		return
	}
	manager.issueSession(writer, request)
	writeJSON(writer, http.StatusOK, map[string]any{"authenticated": true, "username": username})
}

func (manager *authManager) logout(writer http.ResponseWriter, request *http.Request) {
	if cookie, err := request.Cookie(sessionCookie); err == nil {
		manager.sessionMux.Lock()
		delete(manager.sessions, cookie.Value)
		manager.sessionMux.Unlock()
	}
	http.SetCookie(writer, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: request.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writer.WriteHeader(http.StatusNoContent)
}

func (manager *authManager) authenticated(request *http.Request) bool {
	cookie, err := request.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	manager.sessionMux.RLock()
	expires, exists := manager.sessions[cookie.Value]
	manager.sessionMux.RUnlock()
	return exists && time.Now().Before(expires)
}

func (manager *authManager) issueSession(writer http.ResponseWriter, request *http.Request) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	token := base64.RawURLEncoding.EncodeToString(buffer)
	expires := time.Now().Add(sessionLifetime)
	manager.sessionMux.Lock()
	manager.sessions[token] = expires
	manager.sessionMux.Unlock()
	http.SetCookie(writer, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: request.TLS != nil, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(sessionLifetime.Seconds())})
}

func readCredentials(writer http.ResponseWriter, request *http.Request) (credentials, bool) {
	payload, ok := readBoundedBody(writer, request, 8*1024)
	if !ok {
		return credentials{}, false
	}
	var result credentials
	if json.Unmarshal(payload, &result) != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "request body must be valid JSON"})
		return credentials{}, false
	}
	result.Username = strings.TrimSpace(result.Username)
	if len(result.Username) < 1 || len(result.Username) > 64 || len(result.Password) < 12 || len(result.Password) > 256 {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "username is required and password must be at least 12 characters"})
		return credentials{}, false
	}
	return result, true
}

func sameOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return origin == scheme+"://"+request.Host
}
