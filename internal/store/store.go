package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db            *sql.DB
	attachmentDir string
}

type Message struct {
	ID        string `json:"id"`
	LXMFID    string `json:"lxmf_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Peer      string `json:"peer"`
	Direction string `json:"direction"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
	State     string `json:"state"`
	ImagePath string `json:"-"`
	ImageMIME string `json:"image_mime,omitempty"`
	ImageURL  string `json:"image_url,omitempty"`
	AudioPath string `json:"-"`
	AudioMIME string `json:"audio_mime,omitempty"`
	AudioURL  string `json:"audio_url,omitempty"`
}

type Contact struct {
	Address   string `json:"address"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type ConversationState struct {
	Peer       string `json:"peer"`
	LastReadAt int64  `json:"last_read_at"`
	Unread     int64  `json:"unread"`
	UpdatedAt  int64  `json:"updated_at"`
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	attachmentDir := filepath.Join(dataDir, "attachments")
	if err := os.MkdirAll(attachmentDir, 0o700); err != nil {
		return nil, fmt.Errorf("create attachment directory: %w", err)
	}
	databasePath := filepath.Join(dataDir, "websideband.db")
	db, err := sql.Open("sqlite", "file:"+databasePath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	result := &Store{db: db, attachmentDir: attachmentDir}
	if err := result.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return result, nil
}

func (store *Store) Close() error { return store.db.Close() }

func (store *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS contacts (
  address TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  lxmf_id TEXT UNIQUE,
  request_id TEXT UNIQUE,
  peer TEXT NOT NULL,
  direction TEXT NOT NULL CHECK (direction IN ('incoming','outgoing')),
  content TEXT NOT NULL,
  timestamp INTEGER NOT NULL,
  state TEXT NOT NULL,
  image_path TEXT,
  image_mime TEXT,
  audio_path TEXT,
  audio_mime TEXT
);
CREATE INDEX IF NOT EXISTS messages_peer_timestamp ON messages(peer, timestamp);
CREATE TABLE IF NOT EXISTS conversation_state (
  peer TEXT PRIMARY KEY,
  last_read_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS announces (
  destination TEXT PRIMARY KEY,
  display_name TEXT,
  hops INTEGER,
  app_data BLOB,
  received_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS known_destinations (
  destination TEXT PRIMARY KEY,
  hops INTEGER,
  last_seen INTEGER NOT NULL,
  metadata TEXT
);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
INSERT INTO conversation_state(peer,last_read_at,updated_at)
SELECT peer,MAX(timestamp),CAST(strftime('%s','now') AS INTEGER)*1000
FROM messages
WHERE NOT EXISTS (SELECT 1 FROM settings WHERE key='migration.read_state_initialized')
GROUP BY peer
ON CONFLICT(peer) DO NOTHING;
INSERT OR IGNORE INTO settings(key,value,updated_at)
VALUES('migration.read_state_initialized','1',CAST(strftime('%s','now') AS INTEGER)*1000);`
	if _, err := store.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("apply sqlite schema: %w", err)
	}
	return nil
}

func (store *Store) UpsertMessage(ctx context.Context, message Message) error {
	_, err := store.db.ExecContext(ctx, `
INSERT INTO messages(id,lxmf_id,request_id,peer,direction,content,timestamp,state,image_path,image_mime,audio_path,audio_mime)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  lxmf_id=COALESCE(excluded.lxmf_id,messages.lxmf_id),
  request_id=COALESCE(excluded.request_id,messages.request_id),
  content=excluded.content,
  state=excluded.state,
  image_path=COALESCE(excluded.image_path,messages.image_path),
  image_mime=COALESCE(excluded.image_mime,messages.image_mime),
  audio_path=COALESCE(excluded.audio_path,messages.audio_path),
  audio_mime=COALESCE(excluded.audio_mime,messages.audio_mime)`,
		message.ID, nullable(message.LXMFID), nullable(message.RequestID), message.Peer, message.Direction, message.Content,
		message.Timestamp, message.State, nullable(message.ImagePath), nullable(message.ImageMIME), nullable(message.AudioPath), nullable(message.AudioMIME))
	if err != nil {
		return fmt.Errorf("upsert message: %w", err)
	}
	return nil
}

func (store *Store) UpdateMessageState(ctx context.Context, requestID, lxmfID, state string) error {
	if requestID == "" && lxmfID == "" {
		return nil
	}
	_, err := store.db.ExecContext(ctx, `UPDATE messages SET state=?, lxmf_id=COALESCE(NULLIF(?,''),lxmf_id) WHERE request_id=? OR lxmf_id=?`, state, lxmfID, requestID, lxmfID)
	if err != nil {
		return fmt.Errorf("update message state: %w", err)
	}
	return nil
}

func (store *Store) ListMessages(ctx context.Context, limit int) ([]Message, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := store.db.QueryContext(ctx, `SELECT id,lxmf_id,request_id,peer,direction,content,timestamp,state,image_path,image_mime,audio_path,audio_mime FROM (SELECT id,COALESCE(lxmf_id,'') AS lxmf_id,COALESCE(request_id,'') AS request_id,peer,direction,content,timestamp,state,COALESCE(image_path,'') AS image_path,COALESCE(image_mime,'') AS image_mime,COALESCE(audio_path,'') AS audio_path,COALESCE(audio_mime,'') AS audio_mime FROM messages ORDER BY timestamp DESC LIMIT ?) ORDER BY timestamp ASC`, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	messages := make([]Message, 0)
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.ID, &message.LXMFID, &message.RequestID, &message.Peer, &message.Direction, &message.Content, &message.Timestamp, &message.State, &message.ImagePath, &message.ImageMIME, &message.AudioPath, &message.AudioMIME); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (store *Store) ConversationStates(ctx context.Context) ([]ConversationState, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT messages.peer,
       COALESCE(conversation_state.last_read_at,0),
       SUM(CASE WHEN messages.direction='incoming' AND messages.timestamp>COALESCE(conversation_state.last_read_at,0) THEN 1 ELSE 0 END),
       MAX(messages.timestamp)
FROM messages
LEFT JOIN conversation_state ON conversation_state.peer=messages.peer
GROUP BY messages.peer
ORDER BY MAX(messages.timestamp) DESC`)
	if err != nil {
		return nil, fmt.Errorf("list conversation states: %w", err)
	}
	defer rows.Close()
	states := make([]ConversationState, 0)
	for rows.Next() {
		var state ConversationState
		if err := rows.Scan(&state.Peer, &state.LastReadAt, &state.Unread, &state.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan conversation state: %w", err)
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

func (store *Store) MarkConversationRead(ctx context.Context, peer string) (int64, error) {
	var lastMessageAt int64
	if err := store.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(timestamp),0) FROM messages WHERE peer=?`, peer).Scan(&lastMessageAt); err != nil {
		return 0, fmt.Errorf("find latest conversation message: %w", err)
	}
	now := time.Now().UnixMilli()
	_, err := store.db.ExecContext(ctx, `INSERT INTO conversation_state(peer,last_read_at,updated_at) VALUES(?,?,?) ON CONFLICT(peer) DO UPDATE SET last_read_at=MAX(conversation_state.last_read_at,excluded.last_read_at),updated_at=excluded.updated_at`, peer, lastMessageAt, now)
	if err != nil {
		return 0, fmt.Errorf("mark conversation read: %w", err)
	}
	return lastMessageAt, nil
}

func (store *Store) DeleteConversation(ctx context.Context, peer string) (int64, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin conversation deletion: %w", err)
	}
	defer transaction.Rollback()
	rows, err := transaction.QueryContext(ctx, `SELECT COALESCE(image_path,''),COALESCE(audio_path,'') FROM messages WHERE peer=?`, peer)
	if err != nil {
		return 0, fmt.Errorf("list conversation attachments: %w", err)
	}
	paths := make(map[string]struct{})
	for rows.Next() {
		var imagePath, audioPath string
		if err := rows.Scan(&imagePath, &audioPath); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan conversation attachments: %w", err)
		}
		if imagePath != "" {
			paths[imagePath] = struct{}{}
		}
		if audioPath != "" {
			paths[audioPath] = struct{}{}
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	result, err := transaction.ExecContext(ctx, `DELETE FROM messages WHERE peer=?`, peer)
	if err != nil {
		return 0, fmt.Errorf("delete conversation: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted messages: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM conversation_state WHERE peer=?`, peer); err != nil {
		return 0, fmt.Errorf("delete conversation state: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return 0, fmt.Errorf("commit conversation deletion: %w", err)
	}
	for path := range paths {
		var references int
		if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE image_path=? OR audio_path=?`, path, path).Scan(&references); err != nil {
			return deleted, fmt.Errorf("check attachment references: %w", err)
		}
		if references == 0 && filepath.Dir(path) == store.attachmentDir {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return deleted, fmt.Errorf("remove conversation attachment: %w", err)
			}
		}
	}
	return deleted, nil
}

func (store *Store) SaveAttachment(data []byte, extension string) (string, error) {
	if len(data) == 0 {
		return "", errors.New("attachment is empty")
	}
	hash := sha256.Sum256(data)
	name := fmt.Sprintf("%x.%s", hash, extension)
	path := filepath.Join(store.attachmentDir, name)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	temporary, err := os.CreateTemp(store.attachmentDir, ".incoming-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return "", err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", err
	}
	return path, nil
}

func (store *Store) Attachment(ctx context.Context, messageID, kind string) (string, string, error) {
	var path, mime string
	queries := map[string]string{
		"image": `SELECT COALESCE(image_path,''),COALESCE(image_mime,'') FROM messages WHERE id=?`,
		"audio": `SELECT COALESCE(audio_path,''),COALESCE(audio_mime,'') FROM messages WHERE id=?`,
	}
	query, ok := queries[kind]
	if !ok {
		return "", "", sql.ErrNoRows
	}
	err := store.db.QueryRowContext(ctx, query, messageID).Scan(&path, &mime)
	return path, mime, err
}

func (store *Store) UpsertContact(ctx context.Context, contact Contact) error {
	now := time.Now().UnixMilli()
	_, err := store.db.ExecContext(ctx, `INSERT INTO contacts(address,name,created_at,updated_at) VALUES(?,?,?,?) ON CONFLICT(address) DO UPDATE SET name=excluded.name,updated_at=excluded.updated_at`, contact.Address, contact.Name, now, now)
	return err
}

func (store *Store) ListContacts(ctx context.Context) ([]Contact, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT address,name,created_at,updated_at FROM contacts ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contacts := make([]Contact, 0)
	for rows.Next() {
		var contact Contact
		if err := rows.Scan(&contact.Address, &contact.Name, &contact.CreatedAt, &contact.UpdatedAt); err != nil {
			return nil, err
		}
		contacts = append(contacts, contact)
	}
	return contacts, rows.Err()
}

func (store *Store) DeleteContact(ctx context.Context, address string) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM contacts WHERE address=?`, address)
	return err
}

func (store *Store) Setting(ctx context.Context, key string) (string, error) {
	var value string
	err := store.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	return value, err
}

func (store *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, time.Now().UnixMilli())
	return err
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
