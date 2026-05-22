package database

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
}

type User struct {
	ID   int64
	Name string
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		return nil, err
	}
	return db, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

func (d *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			banned INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_users_banned ON users(banned)`,
		`CREATE TABLE IF NOT EXISTS files (
			message_id INTEGER PRIMARY KEY,
			file_name TEXT,
			file_size INTEGER,
			mime_type TEXT,
			secure_hash TEXT NOT NULL,
			owner_id INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_files_owner ON files(owner_id)`,
		`CREATE INDEX IF NOT EXISTS idx_files_hash ON files(secure_hash)`,
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS m3u_sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL UNIQUE,
			last_sync DATETIME,
			last_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS xtream_downloads (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id   INTEGER NOT NULL,
			stream_url  TEXT    NOT NULL,
			name        TEXT    NOT NULL DEFAULT '',
			kind        TEXT    NOT NULL DEFAULT '',
			file_size   INTEGER NOT NULL DEFAULT 0,
			status      TEXT    NOT NULL DEFAULT 'pending',
			error_msg   TEXT    NOT NULL DEFAULT '',
			tg_msg_id   INTEGER NOT NULL DEFAULT 0,
			final_url   TEXT    NOT NULL DEFAULT '',
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(source_id, stream_url)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_xdl_source ON xtream_downloads(source_id, status)`,
		`CREATE TABLE IF NOT EXISTS bot_tokens (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			token      TEXT    NOT NULL UNIQUE,
			username   TEXT    NOT NULL DEFAULT '',
			active     INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, s := range stmts {
		if _, err := d.conn.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w (stmt: %s)", err, s)
		}
	}
	return nil
}

func (d *DB) AddUser(ctx context.Context, id int64, name string) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO users (id, name) VALUES (?, ?)
		 ON CONFLICT(id) DO UPDATE SET name=excluded.name`,
		id, name)
	return err
}

func (d *DB) UserExists(ctx context.Context, id int64) (bool, error) {
	var exists int
	err := d.conn.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ? LIMIT 1`, id).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (d *DB) TotalUsers(ctx context.Context) (int64, error) {
	var n int64
	err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (d *DB) AllUsers(ctx context.Context) ([]User, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT id, name FROM users WHERE banned = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Name); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (d *DB) DeleteUser(ctx context.Context, id int64) error {
	_, err := d.conn.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

func (d *DB) BanUser(ctx context.Context, id int64) error {
	_, err := d.conn.ExecContext(ctx, `UPDATE users SET banned = 1 WHERE id = ?`, id)
	return err
}

func (d *DB) IsBanned(ctx context.Context, id int64) (bool, error) {
	var banned int
	err := d.conn.QueryRowContext(ctx, `SELECT banned FROM users WHERE id = ?`, id).Scan(&banned)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return banned == 1, nil
}

type FileRecord struct {
	MessageID  int64
	FileName   string
	FileSize   int64
	MimeType   string
	SecureHash string
	OwnerID    int64
}

func (d *DB) SaveFile(ctx context.Context, f FileRecord) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO files (message_id, file_name, file_size, mime_type, secure_hash, owner_id)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(message_id) DO UPDATE SET
		   file_name=excluded.file_name,
		   file_size=excluded.file_size,
		   mime_type=excluded.mime_type,
		   secure_hash=excluded.secure_hash,
		   owner_id=excluded.owner_id`,
		f.MessageID, f.FileName, f.FileSize, f.MimeType, f.SecureHash, f.OwnerID)
	return err
}

type Stats struct {
	Files      int64
	Users      int64
	TotalBytes int64
}

func (d *DB) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	if err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(file_size),0) FROM files`).Scan(&s.Files, &s.TotalBytes); err != nil {
		return s, err
	}
	if err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&s.Users); err != nil {
		return s, err
	}
	return s, nil
}

func (d *DB) GetFile(ctx context.Context, messageID int64) (*FileRecord, error) {
	row := d.conn.QueryRowContext(ctx,
		`SELECT message_id, file_name, file_size, mime_type, secure_hash, owner_id
		 FROM files WHERE message_id = ?`, messageID)
	var f FileRecord
	if err := row.Scan(&f.MessageID, &f.FileName, &f.FileSize, &f.MimeType, &f.SecureHash, &f.OwnerID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}
