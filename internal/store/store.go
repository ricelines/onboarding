package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteBusyTimeoutMillis = 5000

const (
	StatePending           = "pending"
	StateMatrixUserCreated = "matrix_user_created"
	StateScenarioCreated   = "scenario_created"
	StateCompleted         = "completed"
)

type UserAgentRecord struct {
	OwnerMatrixUserID       string
	ProvisioningMode        string
	ProvisioningInstanceKey string
	State                   string
	BotUsername             string
	BotPassword             string
	BotUserID               string
	ScenarioID              string
	LastError               string
	CreatedAt               time.Time
	UpdatedAt               time.Time
	CompletedAt             time.Time
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database dir: %w", err)
	}

	dsn, err := sqliteDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	store := &Store{db: db}
	if err := store.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) ReserveInitialUserAgent(
	ctx context.Context,
	ownerMatrixUserID string,
	provisioningMode string,
	provisioningInstanceKey string,
	botUsername string,
	botPassword string,
) (UserAgentRecord, bool, error) {
	record, found, err := s.GetUserAgent(ctx, ownerMatrixUserID, provisioningMode, provisioningInstanceKey)
	if err != nil {
		return UserAgentRecord{}, false, err
	}
	if found {
		return record, false, nil
	}

	now := time.Now().UTC()
	record = UserAgentRecord{
		OwnerMatrixUserID:       ownerMatrixUserID,
		ProvisioningMode:        provisioningMode,
		ProvisioningInstanceKey: provisioningInstanceKey,
		State:                   StatePending,
		BotUsername:             botUsername,
		BotPassword:             botPassword,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := s.SaveUserAgent(ctx, record); err != nil {
		return UserAgentRecord{}, false, err
	}
	return record, true, nil
}

func (s *Store) GetUserAgent(
	ctx context.Context,
	ownerMatrixUserID string,
	provisioningMode string,
	provisioningInstanceKey string,
) (UserAgentRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT owner_matrix_user_id, provisioning_mode, provisioning_instance_key, state,
		       bot_username, bot_password, bot_user_id, scenario_id, last_error,
		       created_at_ms, updated_at_ms, completed_at_ms
		FROM user_agents
		WHERE owner_matrix_user_id = ? AND provisioning_mode = ? AND provisioning_instance_key = ?
	`, ownerMatrixUserID, provisioningMode, provisioningInstanceKey)

	record, err := scanUserAgent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserAgentRecord{}, false, nil
		}
		return UserAgentRecord{}, false, fmt.Errorf("get user agent: %w", err)
	}
	return record, true, nil
}

func (s *Store) ListUserAgents(ctx context.Context, ownerMatrixUserID string) ([]UserAgentRecord, error) {
	query := `
		SELECT owner_matrix_user_id, provisioning_mode, provisioning_instance_key, state,
		       bot_username, bot_password, bot_user_id, scenario_id, last_error,
		       created_at_ms, updated_at_ms, completed_at_ms
		FROM user_agents
	`
	var args []any
	if strings.TrimSpace(ownerMatrixUserID) != "" {
		query += ` WHERE owner_matrix_user_id = ?`
		args = append(args, ownerMatrixUserID)
	}
	query += ` ORDER BY created_at_ms ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list user agents: %w", err)
	}
	defer rows.Close()

	var records []UserAgentRecord
	for rows.Next() {
		record, err := scanUserAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user agent: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user agents: %w", err)
	}
	return records, nil
}

func (s *Store) SaveUserAgent(ctx context.Context, record UserAgentRecord) error {
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO user_agents (
			owner_matrix_user_id,
			provisioning_mode,
			provisioning_instance_key,
			state,
			bot_username,
			bot_password,
			bot_user_id,
			scenario_id,
			last_error,
			created_at_ms,
			updated_at_ms,
			completed_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_matrix_user_id, provisioning_mode, provisioning_instance_key) DO UPDATE SET
			state = excluded.state,
			bot_username = excluded.bot_username,
			bot_password = excluded.bot_password,
			bot_user_id = excluded.bot_user_id,
			scenario_id = excluded.scenario_id,
			last_error = excluded.last_error,
			updated_at_ms = excluded.updated_at_ms,
			completed_at_ms = excluded.completed_at_ms
	`, record.OwnerMatrixUserID,
		record.ProvisioningMode,
		record.ProvisioningInstanceKey,
		record.State,
		record.BotUsername,
		record.BotPassword,
		record.BotUserID,
		record.ScenarioID,
		record.LastError,
		toUnixMillis(record.CreatedAt),
		toUnixMillis(record.UpdatedAt),
		toUnixMillis(record.CompletedAt),
	)
	if err != nil {
		return fmt.Errorf("save user agent: %w", err)
	}
	return nil
}

func (s *Store) init(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS user_agents (
			owner_matrix_user_id TEXT NOT NULL,
			provisioning_mode TEXT NOT NULL,
			provisioning_instance_key TEXT NOT NULL,
			state TEXT NOT NULL,
			bot_username TEXT NOT NULL DEFAULT '',
			bot_password TEXT NOT NULL DEFAULT '',
			bot_user_id TEXT NOT NULL DEFAULT '',
			scenario_id TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			created_at_ms INTEGER NOT NULL,
			updated_at_ms INTEGER NOT NULL,
			completed_at_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (owner_matrix_user_id, provisioning_mode, provisioning_instance_key)
		)
	`)
	if err != nil {
		return fmt.Errorf("initialize sqlite schema: %w", err)
	}
	return nil
}

func sqliteDSN(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve sqlite db path: %w", err)
	}

	u := &url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(absolutePath),
	}
	query := u.Query()
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(FULL)")
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeoutMillis))
	u.RawQuery = query.Encode()
	return u.String(), nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanUserAgent(row scanner) (UserAgentRecord, error) {
	var (
		record        UserAgentRecord
		createdAtMS   int64
		updatedAtMS   int64
		completedAtMS int64
	)
	err := row.Scan(
		&record.OwnerMatrixUserID,
		&record.ProvisioningMode,
		&record.ProvisioningInstanceKey,
		&record.State,
		&record.BotUsername,
		&record.BotPassword,
		&record.BotUserID,
		&record.ScenarioID,
		&record.LastError,
		&createdAtMS,
		&updatedAtMS,
		&completedAtMS,
	)
	if err != nil {
		return UserAgentRecord{}, err
	}
	record.CreatedAt = fromUnixMillis(createdAtMS)
	record.UpdatedAt = fromUnixMillis(updatedAtMS)
	record.CompletedAt = fromUnixMillis(completedAtMS)
	return record, nil
}

func toUnixMillis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func fromUnixMillis(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}
