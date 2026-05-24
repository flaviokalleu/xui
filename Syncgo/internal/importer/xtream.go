// Package importer - Xtream Codes JSON API importer for VOD and series.
package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"syncgo/internal/xui"
)

// ProgressFunc is called after each item during an import.
// done = items processed so far, total = total items, label = current item name.
type ProgressFunc func(done, total int, label string)

// UploadFunc downloads content from srcURL and returns the final stream URL to store in XUI.
// When nil, the Xtream source URL is used directly (no download/upload).
// When set, it is called for every movie/episode before DB insert.
type UploadFunc func(ctx context.Context, srcURL, name string) (streamURL string, err error)

// XtreamCreds holds credentials extracted from an Xtream Codes get.php URL.
type XtreamCreds struct {
	BaseURL  string
	Username string
	Password string
}

// XtreamResult holds statistics from an Xtream import.
type XtreamResult struct {
	MoviesInserted  int
	MoviesUpdated   int
	SeriesInserted  int
	SeriesUpdated   int
	EpisodesAdded   int
	BouquetMoviesID int64
	BouquetSeriesID int64
}

// IsXtreamURL reports whether rawURL is an Xtream Codes get.php URL.
func IsXtreamURL(rawURL string) bool {
	_, err := ParseXtreamURL(rawURL)
	return err == nil
}

// ParseXtreamURL extracts Xtream Codes credentials from a get.php URL.
// Accepts: http://server/get.php?username=X&password=Y&type=m3u_plus
func ParseXtreamURL(rawURL string) (*XtreamCreds, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(u.Path, "get.php") {
		return nil, fmt.Errorf("not an Xtream Codes URL (path must contain get.php)")
	}
	q := u.Query()
	username := q.Get("username")
	password := q.Get("password")
	if username == "" || password == "" {
		return nil, fmt.Errorf("missing username or password in URL")
	}
	base := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	return &XtreamCreds{BaseURL: base, Username: username, Password: password}, nil
}

// OnEpisodeFunc é chamada antes de cada episódio ser baixado, com o nome da série e label do episódio.
type OnEpisodeFunc func(seriesName, episodeLabel string)

// OnXUIUpdateFunc é chamada após inserir ou atualizar um item no XUI.
// inserted=true → novo item; inserted=false → item existente atualizado.
type OnXUIUpdateFunc func(inserted bool)

// OnSeriesStartFunc é chamada antes de processar os episódios de cada série.
// done = séries já concluídas, total = total de séries na lista.
type OnSeriesStartFunc func(done, total int, seriesName string)

// XtreamImporter imports VOD and series from an Xtream Codes provider into XUI.
type XtreamImporter struct {
	xui           *xui.DB
	log           *slog.Logger
	http          *http.Client
	DryRun        bool              // se true, busca mas não insere no banco
	Upload        UploadFunc        // se não nil, chamado para cada item antes do insert
	OnSeriesStart OnSeriesStartFunc // se não nil, chamado antes dos episódios de cada série
	OnEpisode     OnEpisodeFunc     // se não nil, chamado antes de cada episódio iniciar download
	OnXUIUpdate   OnXUIUpdateFunc   // se não nil, chamado após cada insert/update no XUI
}

// NewXtreamImporter creates a new XtreamImporter.
func NewXtreamImporter(db *xui.DB, log *slog.Logger) *XtreamImporter {
	return &XtreamImporter{
		xui:  db,
		log:  log,
		http: &http.Client{Timeout: 120 * time.Second},
	}
}

// NewXtreamImporterDryRun creates an importer that only fetches, without any DB writes.
func NewXtreamImporterDryRun(log *slog.Logger) *XtreamImporter {
	return &XtreamImporter{
		log:    log,
		http:   &http.Client{Timeout: 120 * time.Second},
		DryRun: true,
	}
}

func (x *XtreamImporter) Close() {
	if x.xui != nil {
		_ = x.xui.Close()
		x.xui = nil
	}
}

func (x *XtreamImporter) apiURL(creds *XtreamCreds, action string, extra map[string]string) string {
	p := url.Values{}
	p.Set("username", creds.Username)
	p.Set("password", creds.Password)
	if action != "" {
		p.Set("action", action)
	}
	for k, v := range extra {
		p.Set(k, v)
	}
	return creds.BaseURL + "/player_api.php?" + p.Encode()
}

func (x *XtreamImporter) fetch(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := x.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// Xtream API response types

type xtreamVOD struct {
	StreamID    json.Number `json:"stream_id"`
	Name        string      `json:"name"`
	StreamIcon  string      `json:"stream_icon"`
	Rating      json.Number `json:"rating"`
	Plot        string      `json:"plot"`
	Cast        string      `json:"cast"`
	Director    string      `json:"director"`
	Genre       string      `json:"genre"`
	ReleaseDate string      `json:"release_date"`
	CategoryID  string      `json:"category_id"`
}

type xtreamSeries struct {
	SeriesID    json.Number `json:"series_id"`
	Name        string      `json:"name"`
	Cover       string      `json:"cover"`
	Plot        string      `json:"plot"`
	Cast        string      `json:"cast"`
	Genre       string      `json:"genre"`
	ReleaseDate string      `json:"release_date"`
	Rating      json.Number `json:"rating"`
	CategoryID  string      `json:"category_id"`
}

type xtreamSeriesInfo struct {
	Episodes map[string][]xtreamEpisode `json:"episodes"`
}

type xtreamEpisode struct {
	ID                 json.Number `json:"id"`
	EpisodeNum         json.Number `json:"episode_num"`
	Title              string      `json:"title"`
	Season             json.Number `json:"season"`
	ContainerExtension string      `json:"container_extension"`
}

type xtreamCategory struct {
	CategoryID   string `json:"category_id"`
	CategoryName string `json:"category_name"`
}

// posterPathFromIcon extracts the TMDB poster path from a stream_icon CDN URL.
// "https://image.tmdb.org/t/p/w300/abc123.jpg" → "/abc123.jpg"
var tmdbImgPathRe = regexp.MustCompile(`/t/p/[^/]+(/\S+\.(?:jpg|png|webp))`)

func posterPathFromIcon(icon string) string {
	if m := tmdbImgPathRe.FindStringSubmatch(icon); m != nil {
		return m[1]
	}
	return ""
}

func movieStreamURL(creds *XtreamCreds, streamID int64) string {
	return fmt.Sprintf("%s/movie/%s/%s/%d",
		creds.BaseURL, url.PathEscape(creds.Username), url.PathEscape(creds.Password), streamID)
}

func episodeStreamURL(creds *XtreamCreds, epID int64, ext string) string {
	if ext == "" {
		ext = "mp4"
	}
	return fmt.Sprintf("%s/series/%s/%s/%d.%s",
		creds.BaseURL, url.PathEscape(creds.Username), url.PathEscape(creds.Password), epID, ext)
}

func (x *XtreamImporter) fetchCategories(ctx context.Context, creds *XtreamCreds, action string) (map[string]string, error) {
	var cats []xtreamCategory
	if err := x.fetch(ctx, x.apiURL(creds, action, nil), &cats); err != nil {
		return nil, err
	}
	m := make(map[string]string, len(cats))
	for _, c := range cats {
		m[c.CategoryID] = c.CategoryName
	}
	return m, nil
}

// ImportMovies fetches get_vod_streams and inserts/updates movies in XUI one by one.
// onProgress is called after each movie processed (may be nil).
func (x *XtreamImporter) ImportMovies(ctx context.Context, creds *XtreamCreds, onProgress ProgressFunc) (inserted, updated int, bouquetID int64, err error) {
	catMap, catErr := x.fetchCategories(ctx, creds, "get_vod_categories")
	if catErr != nil {
		x.log.Warn("xtream: could not fetch VOD categories", "err", catErr)
		catMap = map[string]string{}
	}

	var streams []xtreamVOD
	if err = x.fetch(ctx, x.apiURL(creds, "get_vod_streams", nil), &streams); err != nil {
		return 0, 0, 0, fmt.Errorf("get_vod_streams: %w", err)
	}
	total := len(streams)
	x.log.Info("xtream: VOD streams fetched", "count", total)

	if !x.DryRun {
		bouquetID, err = x.xui.GetOrCreateBouquet(ctx, "FILMES")
		if err != nil {
			return 0, 0, 0, fmt.Errorf("bouquet FILMES: %w", err)
		}
	}

	catCache := make(map[string]int64)
	var pendingBouquet []int64

	flushBouquet := func() {
		if !x.DryRun && len(pendingBouquet) > 0 {
			_ = x.xui.AddToBouquet(ctx, bouquetID, xui.BouquetMovies, pendingBouquet...)
			pendingBouquet = pendingBouquet[:0]
		}
	}

	for i, s := range streams {
		if ctx.Err() != nil {
			break
		}
		streamID, _ := s.StreamID.Int64()
		if streamID == 0 {
			continue
		}

		streamURL := movieStreamURL(creds, streamID)

		// Download (e futuramente upload ao Telegram) se configurado
		if x.Upload != nil {
			var upErr error
			streamURL, upErr = x.Upload(ctx, streamURL, s.Name)
			if upErr != nil {
				x.log.Error("xtream: upload movie", "id", streamID, "name", s.Name, "err", upErr)
				if onProgress != nil {
					onProgress(i+1, total, s.Name)
				}
				continue
			}
		}

		if !x.DryRun {
			catName := catMap[s.CategoryID]
			if catName == "" {
				catName = "Filmes"
			}
			xuiCatID, ok := catCache[catName]
			if !ok {
				xuiCatID, _ = x.xui.GetOrCreateCategory(ctx, catName, "movie")
				catCache[catName] = xuiCatID
			}

			rating, _ := s.Rating.Float64()
			year := 0
			if len(s.ReleaseDate) >= 4 {
				fmt.Sscanf(s.ReleaseDate[:4], "%d", &year)
			}

			existingID, exists, checkErr := x.xui.MovieExists(ctx, streamID)
			if checkErr != nil {
				x.log.Error("xtream: check movie exists", "id", streamID, "err", checkErr)
				continue
			}
			if exists {
				if upErr := x.xui.UpdateMovieSource(ctx, existingID, streamURL, false); upErr != nil {
					x.log.Error("xtream: update movie source", "id", streamID, "err", upErr)
					continue
				}
				updated++
				pendingBouquet = append(pendingBouquet, xuiCatID)
				if x.OnXUIUpdate != nil {
					x.OnXUIUpdate(false)
				}
			} else {
				_, insErr := x.xui.InsertMovie(ctx, xui.Movie{
					TMDBID:       streamID,
					Title:        s.Name,
					Description:  s.Plot,
					PosterPath:   posterPathFromIcon(s.StreamIcon),
					ReleaseYear:  year,
					ReleaseDate:  s.ReleaseDate,
					Rating:       rating,
					Director:     s.Director,
					Cast:         s.Cast,
					Genres:       splitGenres(s.Genre),
					StreamSource: streamURL,
					Language:     "pt-BR",
					CategoryIDs:  []int64{xuiCatID},
				})
				if insErr != nil {
					x.log.Error("xtream: insert movie", "id", streamID, "name", s.Name, "err", insErr)
					continue
				}
				inserted++
				pendingBouquet = append(pendingBouquet, xuiCatID)
				if x.OnXUIUpdate != nil {
					x.OnXUIUpdate(true)
				}
			}
			if len(pendingBouquet) >= 200 {
				flushBouquet()
			}
		} else {
			inserted++ // conta como "processado" no dry-run
		}

		if onProgress != nil {
			onProgress(i+1, total, s.Name)
		}
	}
	flushBouquet()

	x.log.Info("xtream: movies import done", "inserted", inserted, "updated", updated)
	return inserted, updated, bouquetID, nil
}

// ImportSeries fetches get_series and imports series + episodes one by one.
// maxEpisodeSeries limits how many series have episodes fetched (0 = all).
// onProgress is called after each series processed (may be nil).
func (x *XtreamImporter) ImportSeries(ctx context.Context, creds *XtreamCreds, maxEpisodeSeries int, onProgress ProgressFunc) (seriesIns, seriesUpd, epAdded int, bouquetID int64, err error) {
	catMap, catErr := x.fetchCategories(ctx, creds, "get_series_categories")
	if catErr != nil {
		x.log.Warn("xtream: could not fetch series categories", "err", catErr)
		catMap = map[string]string{}
	}

	var seriesList []xtreamSeries
	if err = x.fetch(ctx, x.apiURL(creds, "get_series", nil), &seriesList); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("get_series: %w", err)
	}
	total := len(seriesList)
	x.log.Info("xtream: series fetched", "count", total)

	if !x.DryRun {
		bouquetID, err = x.xui.GetOrCreateBouquet(ctx, "SÉRIES")
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("bouquet SÉRIES: %w", err)
		}
	}

	catCache := make(map[string]int64)
	epSeriesCount := 0

	for i, s := range seriesList {
		if ctx.Err() != nil {
			break
		}
		seriesID, _ := s.SeriesID.Int64()
		if seriesID == 0 {
			continue
		}

		var xuiSeriesID int64

		if !x.DryRun {
			catName := catMap[s.CategoryID]
			if catName == "" {
				catName = "Séries"
			}
			xuiCatID, ok := catCache[catName]
			if !ok {
				xuiCatID, _ = x.xui.GetOrCreateCategory(ctx, catName, "series")
				catCache[catName] = xuiCatID
			}

			rating, _ := s.Rating.Float64()
			year := 0
			if len(s.ReleaseDate) >= 4 {
				fmt.Sscanf(s.ReleaseDate[:4], "%d", &year)
			}

			var exists bool
			xuiSeriesID, exists, err = x.xui.SeriesExists(ctx, seriesID)
			if err != nil {
				x.log.Error("xtream: check series exists", "id", seriesID, "err", err)
				continue
			}
			if !exists {
				xuiSeriesID, err = x.xui.InsertSeries(ctx, xui.Series{
					TMDBID:       seriesID,
					Title:        s.Name,
					Description:  s.Plot,
					PosterPath:   posterPathFromIcon(s.Cover),
					FirstAirDate: s.ReleaseDate,
					Year:         year,
					Rating:       rating,
					Genres:       splitGenres(s.Genre),
					Cast:         s.Cast,
					Language:     "pt-BR",
					CategoryIDs:  []int64{xuiCatID},
				})
				if err != nil {
					x.log.Error("xtream: insert series", "id", seriesID, "name", s.Name, "err", err)
					continue
				}
				seriesIns++
			} else {
				seriesUpd++
			}
			_ = x.xui.AddToBouquet(ctx, bouquetID, xui.BouquetSeries, xuiCatID)
		} else {
			xuiSeriesID = seriesID // placeholder no dry-run
			seriesIns++
		}

		// Notifica início do processamento desta série (antes dos episódios).
		if x.OnSeriesStart != nil {
			x.OnSeriesStart(i, total, s.Name)
		}

		// Busca e baixa episódios (Upload é chamado dentro de importEpisodes)
		if maxEpisodeSeries == 0 || epSeriesCount < maxEpisodeSeries {
			eps, epErr := x.importEpisodes(ctx, creds, xuiSeriesID, seriesID, s.Name)
			if epErr != nil {
				x.log.Warn("xtream: import episodes failed", "series_id", seriesID, "err", epErr)
			} else {
				epAdded += eps
			}
			epSeriesCount++

			// Throttle every 20 series to avoid server rate limiting
			if i > 0 && i%20 == 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(300 * time.Millisecond):
				}
			}
		}

		if onProgress != nil {
			onProgress(i+1, total, s.Name)
		}
	}

	x.log.Info("xtream: series import done",
		"series_inserted", seriesIns, "series_updated", seriesUpd, "episodes_added", epAdded)
	return seriesIns, seriesUpd, epAdded, bouquetID, nil
}

func (x *XtreamImporter) importEpisodes(ctx context.Context, creds *XtreamCreds, xuiSeriesID, xtreamSeriesID int64, seriesName string) (int, error) {
	var info xtreamSeriesInfo
	u := x.apiURL(creds, "get_series_info", map[string]string{
		"series_id": strconv.FormatInt(xtreamSeriesID, 10),
	})
	if err := x.fetch(ctx, u, &info); err != nil {
		return 0, err
	}

	added := 0
	for seasonStr, episodes := range info.Episodes {
		season, _ := strconv.Atoi(seasonStr)
		if season == 0 {
			season = 1
		}
		for _, ep := range episodes {
			if ctx.Err() != nil {
				return added, ctx.Err()
			}
			epID, _ := ep.ID.Int64()
			if epID == 0 {
				continue
			}
			epNum, _ := ep.EpisodeNum.Int64()
			if epNum == 0 {
				epNum = 1
			}
			epSeason, _ := ep.Season.Int64()
			if epSeason != 0 {
				season = int(epSeason)
			}
			title := ep.Title
			if title == "" {
				title = fmt.Sprintf("S%02dE%02d", season, epNum)
			}
			epStreamURL := episodeStreamURL(creds, epID, ep.ContainerExtension)

			// Quando Upload está ativo (modo download), a idempotência é pelo SQLite.
			// Quando Upload é nil (só sync XUI), pula episódios já existentes no banco.
			if !x.DryRun && x.Upload == nil {
				if _, exists, err := x.xui.EpisodeExists(ctx, xuiSeriesID, season, int(epNum)); err != nil || exists {
					continue
				}
			}

			// Notifica progresso por episódio antes de baixar
			if x.OnEpisode != nil {
				x.OnEpisode(seriesName, fmt.Sprintf("S%02dE%02d %s", season, int(epNum), title))
			}

			// Download + upload ao Telegram se configurado.
			// O nome do arquivo usa série + S/E para garantir identificação correta,
			// independente do título que a API Xtream retorna para o episódio.
			if x.Upload != nil {
				uploadName := fmt.Sprintf("%s S%02dE%02d", seriesName, season, int(epNum))
				var upErr error
				epStreamURL, upErr = x.Upload(ctx, epStreamURL, uploadName)
				if upErr != nil {
					x.log.Error("xtream: upload episode", "series_id", xuiSeriesID, "s", season, "e", epNum, "err", upErr)
					continue
				}
			}

			// Pula insert no banco em dry-run (download já foi feito acima)
			if x.DryRun {
				added++
				continue
			}

			// Em modo download+XUI: atualiza URL se episódio já existia, insere caso contrário
			if xuiEpID, exists, err := x.xui.EpisodeExists(ctx, xuiSeriesID, season, int(epNum)); err == nil && exists {
				_ = x.xui.UpdateEpisodeSource(ctx, xuiEpID, epStreamURL)
				added++
				if x.OnXUIUpdate != nil {
					x.OnXUIUpdate(false)
				}
				continue
			}
			// Usa S/E como título do episódio no XUI — o título da API Xtream
			// frequentemente contém o nome de outra série ou metadados incorretos.
			xuiTitle := fmt.Sprintf("S%02dE%02d", season, int(epNum))
			_, insErr := x.xui.InsertEpisode(ctx, xui.Episode{
				SeriesID:  xuiSeriesID,
				Season:    season,
				Episode:   int(epNum),
				Title:     xuiTitle,
				StreamURL: epStreamURL,
				Language:  "pt-BR",
			})
			if insErr != nil {
				x.log.Error("xtream: insert episode", "series_id", xuiSeriesID, "s", season, "e", epNum, "err", insErr)
				continue
			}
			added++
			if x.OnXUIUpdate != nil {
				x.OnXUIUpdate(true)
			}
		}
	}
	return added, nil
}

func splitGenres(genre string) []string {
	if genre == "" {
		return nil
	}
	parts := strings.Split(genre, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
