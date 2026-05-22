package database

import (
	"context"
	"database/sql"
	"time"
)

type BotToken struct {
	ID        int64
	Token     string
	Username  string
	Active    bool
	CreatedAt time.Time
}

func (d *DB) AddBotToken(ctx context.Context, token, username string) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO bot_tokens (token, username) VALUES (?, ?)
		 ON CONFLICT(token) DO UPDATE SET username=excluded.username, active=1`,
		token, username)
	return err
}

func (d *DB) ListBotTokens(ctx context.Context) ([]BotToken, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, token, username, active, created_at FROM bot_tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BotToken
	for rows.Next() {
		var bt BotToken
		var active int
		if err := rows.Scan(&bt.ID, &bt.Token, &bt.Username, &active, &bt.CreatedAt); err != nil {
			return nil, err
		}
		bt.Active = active == 1
		out = append(out, bt)
	}
	return out, rows.Err()
}

func (d *DB) ActiveBotTokens(ctx context.Context) ([]BotToken, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, token, username, active, created_at FROM bot_tokens WHERE active=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BotToken
	for rows.Next() {
		var bt BotToken
		var active int
		if err := rows.Scan(&bt.ID, &bt.Token, &bt.Username, &active, &bt.CreatedAt); err != nil {
			return nil, err
		}
		bt.Active = active == 1
		out = append(out, bt)
	}
	return out, rows.Err()
}

func (d *DB) DeactivateBotToken(ctx context.Context, id int64) error {
	_, err := d.conn.ExecContext(ctx, `UPDATE bot_tokens SET active=0 WHERE id=?`, id)
	return err
}

func (d *DB) DeleteBotToken(ctx context.Context, id int64) error {
	_, err := d.conn.ExecContext(ctx, `DELETE FROM bot_tokens WHERE id=?`, id)
	return err
}

func (d *DB) BotTokenByID(ctx context.Context, id int64) (*BotToken, error) {
	var bt BotToken
	var active int
	err := d.conn.QueryRowContext(ctx,
		`SELECT id, token, username, active, created_at FROM bot_tokens WHERE id=?`, id).
		Scan(&bt.ID, &bt.Token, &bt.Username, &active, &bt.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	bt.Active = active == 1
	return &bt, nil
}
