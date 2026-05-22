package database

import (
	"context"
	"strconv"
)

func (d *DB) GetSetting(ctx context.Context, key string) (string, bool) {
	var v string
	err := d.conn.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return "", false
	}
	return v, true
}

func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value)
	return err
}

func (d *DB) GetSettingInt64(ctx context.Context, key string, def int64) int64 {
	v, ok := d.GetSetting(ctx, key)
	if !ok {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func (d *DB) DelSetting(ctx context.Context, key string) error {
	_, err := d.conn.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	return err
}

func (d *DB) AllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
