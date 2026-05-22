package database

import (
	"context"
	"database/sql"
	"time"
)

type M3USource struct {
	ID        int64
	Name      string
	URL       string
	LastSync  *time.Time
	LastCount int
}

func (d *DB) AddM3USource(ctx context.Context, name, url string) (int64, error) {
	res, err := d.conn.ExecContext(ctx,
		`INSERT INTO m3u_sources (name, url) VALUES (?, ?)
		 ON CONFLICT(url) DO UPDATE SET name=excluded.name`,
		name, url)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) ListM3USources(ctx context.Context) ([]M3USource, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, name, url, last_sync, last_count FROM m3u_sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []M3USource
	for rows.Next() {
		var s M3USource
		var ls sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &s.URL, &ls, &s.LastCount); err != nil {
			return nil, err
		}
		if ls.Valid {
			t, err := time.Parse("2006-01-02 15:04:05", ls.String)
			if err == nil {
				s.LastSync = &t
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) GetM3USource(ctx context.Context, id int64) (*M3USource, error) {
	var s M3USource
	var ls sql.NullString
	err := d.conn.QueryRowContext(ctx,
		`SELECT id, name, url, last_sync, last_count FROM m3u_sources WHERE id = ?`, id).
		Scan(&s.ID, &s.Name, &s.URL, &ls, &s.LastCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if ls.Valid {
		t, err := time.Parse("2006-01-02 15:04:05", ls.String)
		if err == nil {
			s.LastSync = &t
		}
	}
	return &s, nil
}

func (d *DB) DeleteM3USource(ctx context.Context, id int64) error {
	_, err := d.conn.ExecContext(ctx, `DELETE FROM m3u_sources WHERE id = ?`, id)
	return err
}

func (d *DB) UpdateM3USourceSync(ctx context.Context, id int64, count int) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE m3u_sources SET last_sync=CURRENT_TIMESTAMP, last_count=? WHERE id=?`,
		count, id)
	return err
}
