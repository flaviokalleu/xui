// Package xui implements MySQL operations for an XtreamUI / XUI ONE panel.
package xui

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"syncgo/internal/tmdb"
)

type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	ServerID int
}

type DB struct {
	conn     *sql.DB
	serverID int
}

func Open(cfg Config) (*DB, error) {
	if cfg.Port == 0 {
		cfg.Port = 3306
	}
	if cfg.ServerID == 0 {
		cfg.ServerID = 1
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&loc=Local&charset=utf8mb4&interpolateParams=true",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database)
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	conn.SetMaxOpenConns(8)
	conn.SetConnMaxLifetime(5 * time.Minute)
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping mysql %s:%d: %w", cfg.Host, cfg.Port, err)
	}
	return &DB{conn: conn, serverID: cfg.ServerID}, nil
}

func (d *DB) Close() error { return d.conn.Close() }

// ---------- categories ----------

func (d *DB) GetOrCreateCategory(ctx context.Context, name, categoryType string) (int64, error) {
	var id int64
	err := d.conn.QueryRowContext(ctx,
		`SELECT id FROM streams_categories WHERE category_name = ? AND category_type = ? LIMIT 1`,
		name, categoryType).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := d.conn.ExecContext(ctx,
		`INSERT INTO streams_categories (category_type, category_name, parent_id, cat_order, is_adult)
		 VALUES (?, ?, 0, 0, 0)`,
		categoryType, name)
	if err != nil {
		return 0, fmt.Errorf("insert category %q: %w", name, err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, _ = d.conn.ExecContext(ctx, `UPDATE streams_categories SET cat_order = id WHERE id = ?`, newID)
	return newID, nil
}

// PreloadCategories returns existing categories keyed by "type|name".
func (d *DB) PreloadCategories(ctx context.Context) (map[string]int64, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT id, category_name, category_type FROM streams_categories`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64, 256)
	for rows.Next() {
		var id int64
		var name, ctype string
		if err := rows.Scan(&id, &name, &ctype); err != nil {
			return nil, err
		}
		out[ctype+"|"+name] = id
	}
	return out, rows.Err()
}

func languageOrDefault(lang string) string {
	if lang != "" {
		return lang
	}
	return "pt-BR"
}

func categoryIDsJSON(ids []int64) string {
	if len(ids) == 0 {
		return "[]"
	}
	out, _ := json.Marshal(ids)
	return string(out)
}

// ---------- bouquets ----------

type bouquetField string

const (
	BouquetMovies   bouquetField = "bouquet_movies"
	BouquetSeries   bouquetField = "bouquet_series"
	BouquetChannels bouquetField = "bouquet_channels"
)

type BouquetInfo struct {
	ID   int64
	Name string
}

func (d *DB) ListBouquets(ctx context.Context) ([]BouquetInfo, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT id, bouquet_name FROM bouquets ORDER BY bouquet_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BouquetInfo
	for rows.Next() {
		var b BouquetInfo
		if err := rows.Scan(&b.ID, &b.Name); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (d *DB) GetOrCreateBouquet(ctx context.Context, name string) (int64, error) {
	var id int64
	err := d.conn.QueryRowContext(ctx, `SELECT id FROM bouquets WHERE bouquet_name = ? LIMIT 1`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := d.conn.ExecContext(ctx,
		`INSERT INTO bouquets (bouquet_name, bouquet_channels, bouquet_movies, bouquet_radios, bouquet_series, bouquet_order)
		 VALUES (?, '[]', '[]', '[]', '[]', 1)`, name)
	if err != nil {
		return 0, fmt.Errorf("insert bouquet %q: %w", name, err)
	}
	id, err = res.LastInsertId()
	return id, err
}

func (d *DB) AddToBouquet(ctx context.Context, bouquetID int64, field bouquetField, ids ...int64) error {
	if len(ids) == 0 {
		return nil
	}
	var raw sql.NullString
	if err := d.conn.QueryRowContext(ctx,
		fmt.Sprintf("SELECT %s FROM bouquets WHERE id = ?", field), bouquetID).Scan(&raw); err != nil {
		return err
	}
	current := []int64{}
	if raw.Valid && raw.String != "" {
		_ = json.Unmarshal([]byte(raw.String), &current)
	}
	seen := make(map[int64]bool, len(current)+len(ids))
	for _, x := range current {
		seen[x] = true
	}
	for _, x := range ids {
		if !seen[x] {
			current = append(current, x)
			seen[x] = true
		}
	}
	out, _ := json.Marshal(current)
	_, err := d.conn.ExecContext(ctx,
		fmt.Sprintf("UPDATE bouquets SET %s = ? WHERE id = ?", field),
		string(out), bouquetID)
	return err
}

// ---------- streams (movies / channels) ----------

type Movie struct {
	TMDBID         int64
	Title          string
	OriginalTitle  string
	Description    string
	PosterPath     string
	BackdropPath   string
	ReleaseYear    int
	ReleaseDate    string
	Rating         float64
	RuntimeMinutes int
	Genres         []string
	Director       string
	Cast           string
	Country        string
	Trailer        string
	StreamSource   string
	Language       string // TMDB language tag, e.g. "pt-BR"
	CategoryIDs    []int64
}

func (d *DB) MovieExists(ctx context.Context, tmdbID int64) (int64, bool, error) {
	var id int64
	err := d.conn.QueryRowContext(ctx, `SELECT id FROM streams WHERE tmdb_id = ? AND type = 2 LIMIT 1`, tmdbID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func (d *DB) InsertMovie(ctx context.Context, m Movie) (int64, error) {
	props := movieProperties(m)
	propsJSON, _ := json.Marshal(props)
	streamSource, _ := json.Marshal([]string{m.StreamSource})
	added := time.Now().Unix()
	poster := tmdb.PosterURL(m.PosterPath)

	res, err := d.conn.ExecContext(ctx, `
		INSERT INTO streams (
			type, category_id, stream_display_name, stream_source, stream_icon, notes,
			enable_transcode, transcode_attributes, custom_ffmpeg, movie_properties, movie_subtitles,
			read_native, target_container, stream_all, remove_subtitles, custom_sid, epg_api, epg_id,
			channel_id, epg_lang, ` + "`order`" + `, auto_restart, transcode_profile_id, gen_timestamps, added,
			series_no, direct_source, tv_archive_duration, tv_archive_server_id, tv_archive_pid,
			vframes_server_id, vframes_pid, movie_symlink, rtmp_output, allow_record, probesize_ondemand,
			custom_map, external_push, delay_minutes, tmdb_language, llod, year, rating, plex_uuid, uuid,
			epg_offset, updated, similar, tmdb_id, adaptive_link, title_sync, fps_restart, fps_threshold, direct_proxy
		) VALUES (
			2, ?, ?, ?, ?, NULL, 0, NULL, NULL, ?, NULL, 0, 'mp4', 0, 0, NULL, 0, NULL, NULL, NULL,
			NULL, NULL, 0, 1, ?, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 128000, NULL, NULL, 0, ?, 0, ?, ?,
			'', NULL, 0, '0000-00-00 00:00:00', NULL, ?, NULL, NULL, 0, 90, 1
		)`,
		categoryIDsJSON(m.CategoryIDs), m.Title, string(streamSource), poster,
		string(propsJSON), added, languageOrDefault(m.Language), m.ReleaseYear, m.Rating, m.TMDBID,
	)
	if err != nil {
		return 0, fmt.Errorf("insert stream movie: %w", err)
	}
	streamID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := d.linkStreamServer(ctx, streamID); err != nil {
		return 0, err
	}
	return streamID, nil
}

// UpdateMovieSource adds an additional stream URL to an existing movie (or replaces).
func (d *DB) UpdateMovieSource(ctx context.Context, streamID int64, url string, appendURL bool) error {
	urls := []string{url}
	if appendURL {
		var raw sql.NullString
		if err := d.conn.QueryRowContext(ctx, `SELECT stream_source FROM streams WHERE id = ?`, streamID).Scan(&raw); err == nil && raw.Valid {
			var current []string
			if err := json.Unmarshal([]byte(raw.String), &current); err == nil {
				urls = appendDedup(current, url)
			}
		}
	}
	out, err := json.Marshal(urls)
	if err != nil {
		return fmt.Errorf("marshal stream_source: %w", err)
	}
	_, err = d.conn.ExecContext(ctx, `UPDATE streams SET stream_source = ? WHERE id = ?`, string(out), streamID)
	return err
}

func appendDedup(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func (d *DB) linkStreamServer(ctx context.Context, streamID int64) error {
	_, err := d.conn.ExecContext(ctx, `
		INSERT INTO streams_servers (
			stream_id, server_id, parent_id, pid, to_analyze, stream_status,
			stream_started, stream_info, monitor_pid, aes_pid, current_source,
			bitrate, progress_info, cc_info, on_demand, delay_pid,
			delay_available_at, pids_create_channel, cchannel_rsources,
			updated, compatible, audio_codec, video_codec, resolution, ondemand_check
		) VALUES (?, ?, NULL, NULL, 0, 0, NULL, NULL, NULL, NULL, NULL, NULL,
			NULL, NULL, 0, NULL, NULL, NULL, NULL, NOW(), 0, NULL, NULL, NULL, NULL)`,
		streamID, d.serverID)
	return err
}

func movieProperties(m Movie) map[string]any {
	runtime := m.RuntimeMinutes
	duration := fmt.Sprintf("%02d:%02d:00", runtime/60, runtime%60)
	poster := tmdb.PosterURL(m.PosterPath)
	return map[string]any{
		"kinopoisk_url":    fmt.Sprintf("https://www.themoviedb.org/movie/%d", m.TMDBID),
		"tmdb_id":          strconv.FormatInt(m.TMDBID, 10),
		"name":             m.Title,
		"o_name":           m.OriginalTitle,
		"cover_big":        poster,
		"movie_image":      poster,
		"release_date":     m.ReleaseDate,
		"episode_run_time": strconv.Itoa(runtime),
		"youtube_trailer":  m.Trailer,
		"director":         m.Director,
		"actors":           m.Cast,
		"cast":             m.Cast,
		"description":      m.Description,
		"plot":             m.Description,
		"country":          m.Country,
		"genre":            strings.Join(m.Genres, ", "),
		"backdrop_path":    []string{tmdb.BackdropURL(m.BackdropPath)},
		"duration_secs":    runtime * 60,
		"duration":         duration,
		"video":            []any{},
		"audio":            []any{},
		"bitrate":          0,
		"rating":           strconv.FormatFloat(m.Rating, 'f', 1, 64),
		"age":              "",
		"mpaa_rating":      "",
	}
}

// ---------- series ----------

type Series struct {
	TMDBID        int64
	Title         string
	OriginalTitle string
	Description   string
	PosterPath    string
	BackdropPath  string
	FirstAirDate  string
	Year          int
	Rating        float64
	Genres        []string
	Cast          string
	Country       string
	Language      string // TMDB language tag, e.g. "pt-BR"
	CategoryIDs   []int64
}

func (d *DB) SeriesExists(ctx context.Context, tmdbID int64) (int64, bool, error) {
	var id int64
	err := d.conn.QueryRowContext(ctx, `SELECT id FROM streams_series WHERE tmdb_id = ? LIMIT 1`, tmdbID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func (d *DB) InsertSeries(ctx context.Context, s Series) (int64, error) {
	poster := tmdb.PosterURL(s.PosterPath)
	backdrops, _ := json.Marshal([]string{tmdb.BackdropURL(s.BackdropPath)})

	res, err := d.conn.ExecContext(ctx, `
		INSERT INTO streams_series (
			title, category_id, cover, cover_big, genre, plot, cast, rating, director,
			release_date, last_modified, tmdb_id, seasons, episode_run_time,
			backdrop_path, youtube_trailer, tmdb_language, year, plex_uuid, similar
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, '', 0, ?, '', ?, ?, '', '')`,
		s.Title,
		categoryIDsJSON(s.CategoryIDs),
		poster,
		poster,
		strings.Join(s.Genres, ", "),
		s.Description,
		s.Cast,
		s.Rating,
		s.FirstAirDate,
		time.Now().Unix(),
		s.TMDBID,
		string(backdrops),
		s.Language,
		s.Year,
	)
	if err != nil {
		return 0, fmt.Errorf("insert series: %w", err)
	}
	return res.LastInsertId()
}

// ---------- episodes ----------

type Episode struct {
	SeriesID   int64
	Season     int
	Episode    int
	Title      string
	StreamURL  string
	PosterPath string
	Plot       string
	AirDate    string
	Runtime    int
	Rating     float64
	Language   string // TMDB language tag, e.g. "pt-BR"
}

func (d *DB) EpisodeExists(ctx context.Context, seriesID int64, season, episode int) (int64, bool, error) {
	var id int64
	// Usa streams_episodes para filtrar por series_id — evita falsos positivos
	// quando duas séries diferentes têm o mesmo número de temporada/episódio.
	err := d.conn.QueryRowContext(ctx,
		`SELECT se.stream_id FROM streams_episodes se
		 WHERE se.series_id = ? AND se.season_num = ? AND se.episode_num = ? LIMIT 1`,
		seriesID, season, episode).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func (d *DB) InsertEpisode(ctx context.Context, e Episode) (int64, error) {
	streamSource, _ := json.Marshal([]string{e.StreamURL})
	poster := tmdb.StillURL(e.PosterPath)
	movieProps := map[string]any{
		"name":             e.Title,
		"plot":             e.Plot,
		"description":      e.Plot,
		"release_date":     e.AirDate,
		"episode_run_time": strconv.Itoa(e.Runtime),
		"duration":         fmt.Sprintf("%02d:%02d:00", e.Runtime/60, e.Runtime%60),
		"duration_secs":    e.Runtime * 60,
		"rating":           strconv.FormatFloat(e.Rating, 'f', 1, 64),
		"movie_image":      poster,
		"cover_big":        poster,
	}
	propsJSON, _ := json.Marshal(movieProps)
	added := time.Now().Unix()

	displayName := e.Title
	if displayName == "" {
		displayName = fmt.Sprintf("Episode %d", e.Episode)
	}

	res, err := d.conn.ExecContext(ctx, `
		INSERT INTO streams (
			type, category_id, stream_display_name, stream_source, stream_icon,
			movie_properties, target_container, added, series_no, direct_source,
			tmdb_language, year, ` + "`order`" + `, gen_timestamps, direct_proxy
		) VALUES (
			5, '[]', ?, ?, ?, ?, 'mp4', ?, ?, 1, ?, 0, 0, 1, 1
		)`,
		displayName, string(streamSource), poster,
		string(propsJSON), added, fmt.Sprintf("%d-%d", e.Season, e.Episode), languageOrDefault(e.Language),
	)
	if err != nil {
		return 0, fmt.Errorf("insert episode: %w", err)
	}
	episodeStreamID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := d.linkStreamServer(ctx, episodeStreamID); err != nil {
		return 0, err
	}

	// streams_episodes maps stream_id <-> series_id.
	_, err = d.conn.ExecContext(ctx,
		`INSERT INTO streams_episodes (season_num, series_id, episode_num, stream_id) VALUES (?, ?, ?, ?)`,
		e.Season, e.SeriesID, e.Episode, episodeStreamID)
	if err != nil {
		return 0, fmt.Errorf("insert streams_episodes: %w", err)
	}
	return episodeStreamID, nil
}

// ---------- channels (live TV) ----------

type Channel struct {
	BaseName    string
	URLs        []string
	LogoURL     string
	CategoryIDs []int64
	ExpiryUnix  int64
}

func (d *DB) ChannelExists(ctx context.Context, name string) (int64, bool, error) {
	var id int64
	err := d.conn.QueryRowContext(ctx,
		`SELECT id FROM streams WHERE stream_display_name = ? AND type = 1 LIMIT 1`, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// UpsertChannel insere ou atualiza um canal. Para importações em lote,
// prefira UpsertChannelCached que evita uma SELECT por canal.
func (d *DB) UpsertChannel(ctx context.Context, c Channel) (int64, bool, error) {
	return d.UpsertChannelCached(ctx, c, nil)
}

// UpsertChannelCached é igual ao UpsertChannel mas aceita um cache pré-carregado
// de canais existentes (nome → id) para eliminar o SELECT de verificação N+1.
func (d *DB) UpsertChannelCached(ctx context.Context, c Channel, cache map[string]int64) (int64, bool, error) {
	urlsJSON, _ := json.Marshal(c.URLs)
	categoryJSON := categoryIDsJSON(c.CategoryIDs)

	// Verifica cache antes de ir ao banco.
	var existingID int64
	var exists bool
	if cache != nil {
		existingID, exists = cache[c.BaseName]
	}
	if !exists {
		var err error
		existingID, exists, err = d.ChannelExists(ctx, c.BaseName)
		if err != nil {
			return 0, false, err
		}
	}

	if exists {
		id := existingID
		_, err := d.conn.ExecContext(ctx,
			`UPDATE streams SET stream_source = ?, category_id = ?, stream_icon = ? WHERE id = ?`,
			string(urlsJSON), categoryJSON, c.LogoURL, id)
		if err != nil {
			return 0, false, err
		}
		var hasServer int
		_ = d.conn.QueryRowContext(ctx, `SELECT 1 FROM streams_servers WHERE stream_id = ? LIMIT 1`, id).Scan(&hasServer)
		if hasServer == 0 {
			_ = d.linkStreamServer(ctx, id)
		}
		return id, false, nil
	}

	res, err := d.conn.ExecContext(ctx, `
		INSERT INTO streams (
			type, category_id, stream_display_name, stream_source, stream_icon,
			channel_id, `+"`order`"+`, gen_timestamps, allow_record, target_container,
			tmdb_language, direct_source, direct_proxy
		) VALUES (1, ?, ?, ?, ?, NULL, 1, 1, 1, 'mp4', 'pt-BR', 1, 1)`,
		categoryJSON, c.BaseName, string(urlsJSON), c.LogoURL,
	)
	if err != nil {
		return 0, false, fmt.Errorf("insert channel %q: %w", c.BaseName, err)
	}
	streamID, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	if err := d.linkStreamServer(ctx, streamID); err != nil {
		return 0, false, err
	}
	return streamID, true, nil
}

// PreloadChannels returns existing live-TV channels keyed by stream_display_name.
func (d *DB) PreloadChannels(ctx context.Context) (map[string]int64, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT id, stream_display_name FROM streams WHERE type = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64, 256)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
}
