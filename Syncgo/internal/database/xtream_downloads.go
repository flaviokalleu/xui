package database

import (
	"context"
	"database/sql"
	"time"
)

// XtreamDownload registra o estado de download de um item Xtream.
type XtreamDownload struct {
	ID        int64
	SourceID  int64
	StreamURL string
	Name      string
	Kind      string // "movie" | "episode"
	FileSize  int64
	Status    string // "done" | "error" | "pending"
	ErrorMsg  string
	TgMsgID   int64
	FinalURL  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// XtreamDownloadSave insere ou atualiza um registro de download Xtream.
func (d *DB) XtreamDownloadSave(ctx context.Context, r XtreamDownload) error {
	_, err := d.conn.ExecContext(ctx, `
		INSERT INTO xtream_downloads
			(source_id, stream_url, name, kind, file_size, status, error_msg, tg_msg_id, final_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(source_id, stream_url) DO UPDATE SET
			name      = excluded.name,
			file_size = excluded.file_size,
			status    = excluded.status,
			error_msg = excluded.error_msg,
			tg_msg_id = excluded.tg_msg_id,
			final_url = excluded.final_url,
			updated_at = CURRENT_TIMESTAMP`,
		r.SourceID, r.StreamURL, r.Name, r.Kind,
		r.FileSize, r.Status, r.ErrorMsg, r.TgMsgID, r.FinalURL,
	)
	return err
}

// XtreamDownloadGet retorna o registro de um download, se existir.
func (d *DB) XtreamDownloadGet(ctx context.Context, sourceID int64, streamURL string) (*XtreamDownload, error) {
	row := d.conn.QueryRowContext(ctx, `
		SELECT id, source_id, stream_url, name, kind, file_size, status, error_msg, tg_msg_id, final_url, created_at, updated_at
		FROM xtream_downloads
		WHERE source_id = ? AND stream_url = ?`, sourceID, streamURL)
	var r XtreamDownload
	err := row.Scan(&r.ID, &r.SourceID, &r.StreamURL, &r.Name, &r.Kind,
		&r.FileSize, &r.Status, &r.ErrorMsg, &r.TgMsgID, &r.FinalURL,
		&r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// XtreamDownloadStats retorna contagens por status para uma fonte.
func (d *DB) XtreamDownloadStats(ctx context.Context, sourceID int64) (done, failed, pending int64, err error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM xtream_downloads WHERE source_id = ? GROUP BY status`, sourceID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int64
		if scanErr := rows.Scan(&status, &count); scanErr != nil {
			continue
		}
		switch status {
		case "done":
			done = count
		case "error":
			failed = count
		default:
			pending += count
		}
	}
	err = rows.Err()
	return
}

// XtreamDownloadList retorna todos os downloads de uma fonte (opcionalmente filtrado por status).
func (d *DB) XtreamDownloadList(ctx context.Context, sourceID int64, status string) ([]XtreamDownload, error) {
	q := `SELECT id, source_id, stream_url, name, kind, file_size, status, error_msg, tg_msg_id, final_url, created_at, updated_at
		  FROM xtream_downloads WHERE source_id = ?`
	args := []any{sourceID}
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	q += " ORDER BY id"

	rows, err := d.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []XtreamDownload
	for rows.Next() {
		var r XtreamDownload
		if err := rows.Scan(&r.ID, &r.SourceID, &r.StreamURL, &r.Name, &r.Kind,
			&r.FileSize, &r.Status, &r.ErrorMsg, &r.TgMsgID, &r.FinalURL,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
