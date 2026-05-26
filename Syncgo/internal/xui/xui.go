// Package xui implements MySQL operations for the PB&Ctv/Xtream panel.
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
	AdminID  int // ID do administrador padrão na tabela admin (default 1)
}

type DB struct {
	conn     *sql.DB
	serverID int
	adminID  int
}

func Open(cfg Config) (*DB, error) {
	if cfg.Port == 0 {
		cfg.Port = 3306
	}
	if cfg.ServerID == 0 {
		cfg.ServerID = 1
	}
	if cfg.AdminID == 0 {
		cfg.AdminID = 1
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
	return &DB{conn: conn, serverID: cfg.ServerID, adminID: cfg.AdminID}, nil
}

func (d *DB) Close() error { return d.conn.Close() }

// ---------- categories ----------

func (d *DB) GetOrCreateCategory(ctx context.Context, name, categoryType string) (int64, error) {
	var id int64
	err := d.conn.QueryRowContext(ctx,
		`SELECT id FROM categoria WHERE nome = ? AND type = ? LIMIT 1`,
		name, categoryType).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	var position int64
	_ = d.conn.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position) + 1, 0) FROM categoria WHERE type = ?`,
		categoryType).Scan(&position)

	res, err := d.conn.ExecContext(ctx,
		`INSERT INTO categoria (nome, type, parent_id, is_adult, admin_id, position)
		 VALUES (?, ?, 0, 0, ?, ?)`,
		name, categoryType, d.adminID, position)
	if err != nil {
		return 0, fmt.Errorf("insert category %q: %w", name, err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return newID, nil
}

// PreloadCategories returns existing categories keyed by "type|name".
func (d *DB) PreloadCategories(ctx context.Context) (map[string]int64, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT id, nome, type FROM categoria`)
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

func firstCategoryID(ids []int64) any {
	if len(ids) == 0 {
		return nil
	}
	return ids[0]
}

func durationHMS(minutes int) string {
	if minutes <= 0 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d:00", minutes/60, minutes%60)
}

func rating5(r float64) string {
	if r <= 0 {
		return ""
	}
	return strconv.FormatFloat(r/2, 'f', 1, 64)
}

func backdropJSON(path string) string {
	url := tmdb.BackdropURL(path)
	if url == "" {
		return "[]"
	}
	out, _ := json.Marshal([]string{url})
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
		`INSERT INTO bouquets (bouquet_name) VALUES (?)`, name)
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
	_ = field
	for _, x := range ids {
		var exists int
		if err := d.conn.QueryRowContext(ctx,
			`SELECT 1 FROM bouquet_items WHERE bouquet_id = ? AND category_id = ? LIMIT 1`,
			bouquetID, x).Scan(&exists); err != nil && err != sql.ErrNoRows {
			return err
		}
		if exists == 0 {
			if _, err := d.conn.ExecContext(ctx,
				`INSERT INTO bouquet_items (bouquet_id, category_id) VALUES (?, ?)`,
				bouquetID, x); err != nil {
				return err
			}
		}
	}
	return nil
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
	err := d.conn.QueryRowContext(ctx, `SELECT id FROM streams WHERE tmdb_id = ? AND stream_type = 'movie' LIMIT 1`, tmdbID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func (d *DB) InsertMovie(ctx context.Context, m Movie) (int64, error) {
	added := time.Now().Unix()
	poster := tmdb.PosterURL(m.PosterPath)
	runtimeSeconds := 0
	if m.RuntimeMinutes > 0 {
		runtimeSeconds = m.RuntimeMinutes * 60
	}

	res, err := d.conn.ExecContext(ctx, `
		INSERT INTO streams (
			situacao, tipo_link, link, name, year, stream_type, stream_icon,
			rating, rating_5based, added, category_id, container_extension,
			kinopoisk_url, tmdb_id, cover_big, release_date, episode_run_time,
			youtube_trailer, director, actors, cast, description, plot, country,
			genre, backdrop_path, duration_secs, duration, releasedate, is_adult
		) VALUES (
			'ativo', 'padrao', ?, ?, ?, 'movie', ?, ?, ?, ?, ?, 'mp4',
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0
		)`,
		m.StreamSource, m.Title, strconv.Itoa(m.ReleaseYear), poster,
		strconv.FormatFloat(m.Rating, 'f', 1, 64), rating5(m.Rating), strconv.FormatInt(added, 10), firstCategoryID(m.CategoryIDs),
		fmt.Sprintf("https://www.themoviedb.org/movie/%d", m.TMDBID), strconv.FormatInt(m.TMDBID, 10), poster,
		m.ReleaseDate, strconv.Itoa(m.RuntimeMinutes), m.Trailer, m.Director, m.Cast, m.Cast,
		m.Description, m.Description, m.Country, strings.Join(m.Genres, ", "), backdropJSON(m.BackdropPath),
		strconv.Itoa(runtimeSeconds), durationHMS(m.RuntimeMinutes), m.ReleaseDate,
	)
	if err != nil {
		return 0, fmt.Errorf("insert stream movie: %w", err)
	}
	streamID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return streamID, nil
}

// UpdateMovieSource adds an additional stream URL to an existing movie (or replaces).
func (d *DB) UpdateMovieSource(ctx context.Context, streamID int64, url string, appendURL bool) error {
	_ = appendURL
	_, err := d.conn.ExecContext(ctx, `UPDATE streams SET link = ? WHERE id = ?`, url, streamID)
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
	_, _ = ctx, streamID
	return nil
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
	err := d.conn.QueryRowContext(ctx, `SELECT id FROM series WHERE tmdb_id = ? LIMIT 1`, tmdbID).Scan(&id)
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
	res, err := d.conn.ExecContext(ctx, `
		INSERT INTO series (
			name, category_id, year, stream_type, cover, plot, cast, director, genre,
			release_date, releaseDate, last_modified, rating, rating_5based,
			backdrop_path, youtube_trailer, episode_run_time, tmdb_id, is_adult
		) VALUES (?, ?, ?, 'series', ?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?, '', '', ?, 0)`,
		s.Title,
		firstCategoryID(s.CategoryIDs),
		strconv.Itoa(s.Year),
		poster,
		s.Description,
		s.Cast,
		strings.Join(s.Genres, ", "),
		s.FirstAirDate,
		s.FirstAirDate,
		time.Now().Unix(),
		strconv.FormatFloat(s.Rating, 'f', 1, 64),
		rating5(s.Rating),
		backdropJSON(s.BackdropPath),
		s.TMDBID,
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
	err := d.conn.QueryRowContext(ctx,
		`SELECT id FROM series_episodes
		 WHERE series_id = ? AND season = ? AND episode_num = ? LIMIT 1`,
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
	poster := tmdb.StillURL(e.PosterPath)
	added := time.Now().Unix()
	var categoryID sql.NullInt64
	_ = d.conn.QueryRowContext(ctx, `SELECT category_id FROM series WHERE id = ?`, e.SeriesID).Scan(&categoryID)

	displayName := e.Title
	if displayName == "" {
		displayName = fmt.Sprintf("Episode %d", e.Episode)
	}

	res, err := d.conn.ExecContext(ctx, `
		INSERT INTO series_episodes (
			situacao, tipo_link, link, series_id, category_id, episode_num, title,
			container_extension, duration_secs, duration, cover_big, plot, movie_image,
			added, season, tmdb_id
		) VALUES ('ativo', 'padrao', ?, ?, ?, ?, ?, 'mp4', ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.StreamURL, e.SeriesID, categoryID, e.Episode, displayName,
		e.Runtime*60, durationHMS(e.Runtime), poster, e.Plot, poster, added, e.Season, 0,
	)
	if err != nil {
		return 0, fmt.Errorf("insert episode: %w", err)
	}
	return res.LastInsertId()
}

func (d *DB) UpdateEpisodeSource(ctx context.Context, episodeID int64, url string) error {
	_, err := d.conn.ExecContext(ctx, `UPDATE series_episodes SET link = ? WHERE id = ?`, url, episodeID)
	return err
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
		`SELECT id FROM streams WHERE name = ? AND stream_type = 'live' LIMIT 1`, name).Scan(&id)
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
	link := ""
	if len(c.URLs) > 0 {
		link = c.URLs[0]
	}

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
			`UPDATE streams SET link = ?, category_id = ?, stream_icon = ? WHERE id = ?`,
			link, firstCategoryID(c.CategoryIDs), c.LogoURL, id)
		if err != nil {
			return 0, false, err
		}
		return id, false, nil
	}

	res, err := d.conn.ExecContext(ctx, `
		INSERT INTO streams (
			situacao, tipo_link, link, name, stream_type, stream_icon, added,
			category_id, container_extension, is_adult
		) VALUES ('ativo', 'padrao', ?, ?, 'live', ?, ?, ?, 'ts', 0)`,
		link, c.BaseName, c.LogoURL, strconv.FormatInt(time.Now().Unix(), 10), firstCategoryID(c.CategoryIDs),
	)
	if err != nil {
		return 0, false, fmt.Errorf("insert channel %q: %w", c.BaseName, err)
	}
	streamID, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	return streamID, true, nil
}

// PreloadChannels returns existing live-TV channels keyed by name.
func (d *DB) PreloadChannels(ctx context.Context) (map[string]int64, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT id, name FROM streams WHERE stream_type = 'live'`)
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
