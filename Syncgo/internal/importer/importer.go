// Package importer wires parser → TMDB → XUI database.
package importer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"syncgo/internal/parser"
	"syncgo/internal/tmdb"
	"syncgo/internal/xui"
)

type Importer struct {
	xui      *xui.DB
	tmdb     *tmdb.Client
	reloader *xui.Reloader
	log      *slog.Logger

	mu                sync.Mutex
	moviesBouquet     int64
	moviesBouquetTime time.Time
	seriesBouquet     int64
	seriesBouquetTime time.Time
}

const bouquetCacheTTL = 5 * time.Minute

func New(x *xui.DB, t *tmdb.Client, reloader *xui.Reloader, log *slog.Logger) *Importer {
	return &Importer{xui: x, tmdb: t, reloader: reloader, log: log}
}

func (i *Importer) Close() {
	if i.xui != nil {
		_ = i.xui.Close()
		i.xui = nil
	}
}

func (i *Importer) triggerReload() {
	if i.reloader != nil {
		i.reloader.Trigger()
	}
}

type Result struct {
	Kind        string // "movie" | "episode" | "skipped"
	TMDBID      int64
	StreamID    int64
	SeriesID    int64
	Season      int
	Episode     int
	Title       string
	WasExisting bool
	Reason      string
}

// ListBouquets returns existing bouquets from XUI for user selection.
func (i *Importer) ListBouquets(ctx context.Context) ([]xui.BouquetInfo, error) {
	return i.xui.ListBouquets(ctx)
}

// HandleUpload is called after a media upload — fileName from Telegram, streamURL from Syncgo.
// bouquetID: ID do bouquet selecionado pelo usuário (0 = usar padrão FILMES/SÉRIES).
func (i *Importer) HandleUpload(ctx context.Context, fileName, streamURL string, bouquetID int64) (*Result, error) {
	if fileName == "" {
		return &Result{Kind: "skipped", Reason: "empty filename"}, nil
	}
	res := parser.Parse(fileName)
	if res.Kind == parser.KindUnknown {
		return &Result{Kind: "skipped", Reason: "filename does not match known patterns"}, nil
	}

	switch res.Kind {
	case parser.KindMovie:
		return i.handleMovie(ctx, res.TMDBID, streamURL, bouquetID)
	case parser.KindEpisode:
		return i.handleEpisode(ctx, res.TMDBID, res.Season, res.Episode, streamURL, bouquetID)
	}
	return &Result{Kind: "skipped"}, nil
}

func (i *Importer) handleMovie(ctx context.Context, tmdbID int64, streamURL string, bouquetID int64) (*Result, error) {
	if existingID, exists, err := i.xui.MovieExists(ctx, tmdbID); err != nil {
		return nil, fmt.Errorf("check movie exists: %w", err)
	} else if exists {
		if err := i.xui.UpdateMovieSource(ctx, existingID, streamURL, true); err != nil {
			return nil, fmt.Errorf("update movie source: %w", err)
		}
		i.log.Info("movie already exists, source appended", "tmdb", tmdbID, "stream_id", existingID)
		return &Result{Kind: "movie", TMDBID: tmdbID, StreamID: existingID, WasExisting: true}, nil
	}

	movie, err := i.tmdb.GetMovie(ctx, tmdbID)
	if err != nil {
		return nil, fmt.Errorf("tmdb movie %d: %w", tmdbID, err)
	}
	director, cast, credErr := i.tmdb.GetMovieCredits(ctx, tmdbID)
	if credErr != nil {
		i.log.Warn("tmdb credits unavailable", "tmdb", tmdbID, "err", credErr)
	}

	genreNames := tmdb.GenreNames(movie.Genres)
	categoryIDs := make([]int64, 0, len(genreNames)+1)
	for _, g := range genreNames {
		if id, err := i.xui.GetOrCreateCategory(ctx, g, "movie"); err == nil {
			categoryIDs = append(categoryIDs, id)
		}
	}
	if len(categoryIDs) == 0 {
		if id, err := i.xui.GetOrCreateCategory(ctx, "Filmes", "movie"); err == nil {
			categoryIDs = append(categoryIDs, id)
		}
	}

	year := 0
	if len(movie.ReleaseDate) >= 4 {
		fmt.Sscanf(movie.ReleaseDate[:4], "%d", &year)
	}
	countries := []string{}
	for _, c := range movie.ProductionCountries {
		countries = append(countries, c.Name)
	}

	streamID, err := i.xui.InsertMovie(ctx, xui.Movie{
		TMDBID:         tmdbID,
		Title:          movie.Title,
		OriginalTitle:  movie.OriginalTitle,
		Description:    movie.Overview,
		PosterPath:     movie.PosterPath,
		BackdropPath:   movie.BackdropPath,
		ReleaseYear:    year,
		ReleaseDate:    movie.ReleaseDate,
		Rating:         movie.VoteAverage,
		RuntimeMinutes: movie.Runtime,
		Genres:         genreNames,
		Director:       director,
		Cast:           cast,
		Country:        strings.Join(countries, ", "),
		StreamSource:   streamURL,
		Language:       i.tmdb.Language(),
		CategoryIDs:    categoryIDs,
	})
	if err != nil {
		return nil, err
	}

	bqID := bouquetID
	if bqID == 0 {
		bqID, _ = i.getMoviesBouquet(ctx)
	}
	if bqID != 0 {
		_ = i.xui.AddToBouquet(ctx, bqID, xui.BouquetMovies, categoryIDs...)
	}

	i.triggerReload()
	i.log.Info("movie inserted", "tmdb", tmdbID, "stream_id", streamID, "title", movie.Title)
	return &Result{Kind: "movie", TMDBID: tmdbID, StreamID: streamID, Title: movie.Title}, nil
}

func (i *Importer) handleEpisode(ctx context.Context, tmdbID int64, season, episode int, streamURL string, bouquetID int64) (*Result, error) {
	seriesID, exists, err := i.xui.SeriesExists(ctx, tmdbID)
	if err != nil {
		return nil, fmt.Errorf("check series exists: %w", err)
	}
	if !exists {
		show, err := i.tmdb.GetTVShow(ctx, tmdbID)
		if err != nil {
			return nil, fmt.Errorf("tmdb tv %d: %w", tmdbID, err)
		}
		cast, credErr := i.tmdb.GetTVCredits(ctx, tmdbID)
		if credErr != nil {
			i.log.Warn("tmdb tv credits unavailable", "tmdb", tmdbID, "err", credErr)
		}

		genreNames := tmdb.GenreNames(show.Genres)
		categoryIDs := make([]int64, 0, len(genreNames)+1)
		for _, g := range genreNames {
			if id, err := i.xui.GetOrCreateCategory(ctx, g, "series"); err == nil {
				categoryIDs = append(categoryIDs, id)
			}
		}
		if len(categoryIDs) == 0 {
			if id, err := i.xui.GetOrCreateCategory(ctx, "Séries", "series"); err == nil {
				categoryIDs = append(categoryIDs, id)
			}
		}

		year := 0
		if len(show.FirstAirDate) >= 4 {
			fmt.Sscanf(show.FirstAirDate[:4], "%d", &year)
		}

		seriesID, err = i.xui.InsertSeries(ctx, xui.Series{
			TMDBID:        tmdbID,
			Title:         show.Name,
			OriginalTitle: show.OriginalName,
			Description:   show.Overview,
			PosterPath:    show.PosterPath,
			BackdropPath:  show.BackdropPath,
			FirstAirDate:  show.FirstAirDate,
			Year:          year,
			Rating:        show.VoteAverage,
			Genres:        genreNames,
			Cast:          cast,
			Country:       strings.Join(show.OriginCountry, ", "),
			CategoryIDs:   categoryIDs,
			Language:      i.tmdb.Language(),
		})
		if err != nil {
			return nil, fmt.Errorf("insert series: %w", err)
		}
		i.log.Info("series inserted", "tmdb", tmdbID, "series_id", seriesID, "title", show.Name)

		bqID := bouquetID
		if bqID == 0 {
			bqID, _ = i.getSeriesBouquet(ctx)
		}
		if bqID != 0 {
			_ = i.xui.AddToBouquet(ctx, bqID, xui.BouquetSeries, categoryIDs...)
		}
	}

	if existingEpID, epExists, err := i.xui.EpisodeExists(ctx, seriesID, season, episode); err != nil {
		return nil, fmt.Errorf("check episode exists: %w", err)
	} else if epExists {
		i.log.Info("episode already exists", "series_id", seriesID, "s", season, "e", episode, "stream_id", existingEpID)
		return &Result{Kind: "episode", TMDBID: tmdbID, SeriesID: seriesID, StreamID: existingEpID, Season: season, Episode: episode, WasExisting: true}, nil
	}

	ep, err := i.tmdb.GetEpisode(ctx, tmdbID, season, episode)
	epData := xui.Episode{
		SeriesID:  seriesID,
		Season:    season,
		Episode:   episode,
		StreamURL: streamURL,
		Language:  i.tmdb.Language(),
	}
	if err == nil && ep != nil {
		epData.Title = ep.Name
		epData.Plot = ep.Overview
		epData.PosterPath = ep.StillPath
		epData.AirDate = ep.AirDate
		epData.Runtime = ep.Runtime
		epData.Rating = ep.VoteAverage
	}
	if epData.Title == "" {
		epData.Title = fmt.Sprintf("S%02dE%02d", season, episode)
	}
	streamID, err := i.xui.InsertEpisode(ctx, epData)
	if err != nil {
		return nil, fmt.Errorf("insert episode: %w", err)
	}
	i.triggerReload()
	i.log.Info("episode inserted", "series_id", seriesID, "s", season, "e", episode, "stream_id", streamID, "title", epData.Title)
	return &Result{Kind: "episode", TMDBID: tmdbID, SeriesID: seriesID, StreamID: streamID, Season: season, Episode: episode, Title: epData.Title}, nil
}

func (i *Importer) getMoviesBouquet(ctx context.Context) (int64, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.moviesBouquet != 0 && time.Since(i.moviesBouquetTime) < bouquetCacheTTL {
		return i.moviesBouquet, nil
	}
	id, err := i.xui.GetOrCreateBouquet(ctx, "FILMES")
	if err == nil {
		i.moviesBouquet = id
		i.moviesBouquetTime = time.Now()
	}
	return id, err
}

func (i *Importer) getSeriesBouquet(ctx context.Context) (int64, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.seriesBouquet != 0 && time.Since(i.seriesBouquetTime) < bouquetCacheTTL {
		return i.seriesBouquet, nil
	}
	id, err := i.xui.GetOrCreateBouquet(ctx, "SÉRIES")
	if err == nil {
		i.seriesBouquet = id
		i.seriesBouquetTime = time.Now()
	}
	return id, err
}
