// Package tmdb is a tiny client for The Movie Database (TMDB) v3 REST API.
package tmdb

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const cacheCapacity = 4096

const baseURL = "https://api.themoviedb.org/3"

type cacheEntry struct {
	key  string
	body []byte
}

type Client struct {
	apiKey   string
	language string
	http     *http.Client
	mu       sync.Mutex
	cache    map[string]*list.Element
	lru      *list.List
}

func (c *Client) Language() string { return c.language }

func New(apiKey, language string) *Client {
	if language == "" {
		language = "pt-BR"
	}
	return &Client{
		apiKey:   apiKey,
		language: language,
		http:     &http.Client{Timeout: 20 * time.Second},
		cache:    make(map[string]*list.Element, cacheCapacity),
		lru:      list.New(),
	}
}

func (c *Client) cacheGet(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.cache[key]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(el)
	return el.Value.(*cacheEntry).body, true
}

func (c *Client) cachePut(key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.cache[key]; ok {
		el.Value.(*cacheEntry).body = body
		c.lru.MoveToFront(el)
		return
	}
	el := c.lru.PushFront(&cacheEntry{key: key, body: body})
	c.cache[key] = el
	for c.lru.Len() > cacheCapacity {
		old := c.lru.Back()
		if old == nil {
			break
		}
		delete(c.cache, old.Value.(*cacheEntry).key)
		c.lru.Remove(old)
	}
}

type Movie struct {
	ID                  int64   `json:"id"`
	Title               string  `json:"title"`
	OriginalTitle       string  `json:"original_title"`
	Overview            string  `json:"overview"`
	PosterPath          string  `json:"poster_path"`
	BackdropPath        string  `json:"backdrop_path"`
	ReleaseDate         string  `json:"release_date"`
	Runtime             int     `json:"runtime"`
	VoteAverage         float64 `json:"vote_average"`
	Genres              []Genre `json:"genres"`
	ProductionCountries []struct {
		Name string `json:"name"`
	} `json:"production_countries"`
}

type Genre struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type TVShow struct {
	ID               int64       `json:"id"`
	Name             string      `json:"name"`
	OriginalName     string      `json:"original_name"`
	Overview         string      `json:"overview"`
	PosterPath       string      `json:"poster_path"`
	BackdropPath     string      `json:"backdrop_path"`
	FirstAirDate     string      `json:"first_air_date"`
	VoteAverage      float64     `json:"vote_average"`
	Genres           []Genre     `json:"genres"`
	NumberOfSeasons  int         `json:"number_of_seasons"`
	NumberOfEpisodes int         `json:"number_of_episodes"`
	OriginCountry    []string    `json:"origin_country"`
	Networks         []NamedItem `json:"networks"`
}

type NamedItem struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Episode struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	StillPath     string  `json:"still_path"`
	AirDate       string  `json:"air_date"`
	EpisodeNumber int     `json:"episode_number"`
	SeasonNumber  int     `json:"season_number"`
	Runtime       int     `json:"runtime"`
	VoteAverage   float64 `json:"vote_average"`
}

type Credits struct {
	Cast []struct {
		Name string `json:"name"`
	} `json:"cast"`
	Crew []struct {
		Name string `json:"name"`
		Job  string `json:"job"`
	} `json:"crew"`
}

func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	if c.apiKey == "" {
		return fmt.Errorf("TMDB_API_KEY is not configured")
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("api_key", c.apiKey)
	if params.Get("language") == "" {
		params.Set("language", c.language)
	}
	full := baseURL + path + "?" + params.Encode()

	if cached, ok := c.cacheGet(full); ok {
		return json.Unmarshal(cached, out)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("tmdb %s: status %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	c.cachePut(full, body)
	return json.Unmarshal(body, out)
}

func (c *Client) GetMovie(ctx context.Context, tmdbID int64) (*Movie, error) {
	var m Movie
	if err := c.get(ctx, fmt.Sprintf("/movie/%d", tmdbID), nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (c *Client) GetMovieCredits(ctx context.Context, tmdbID int64) (director, cast string, err error) {
	var cr Credits
	if err = c.get(ctx, fmt.Sprintf("/movie/%d/credits", tmdbID), nil, &cr); err != nil {
		return "", "", err
	}
	dirs := []string{}
	for _, p := range cr.Crew {
		if p.Job == "Director" {
			dirs = append(dirs, p.Name)
		}
	}
	director = strings.Join(dirs, ", ")
	castNames := []string{}
	for i, p := range cr.Cast {
		if i >= 5 {
			break
		}
		castNames = append(castNames, p.Name)
	}
	cast = strings.Join(castNames, ", ")
	return director, cast, nil
}

func (c *Client) GetTVShow(ctx context.Context, tmdbID int64) (*TVShow, error) {
	var s TVShow
	if err := c.get(ctx, fmt.Sprintf("/tv/%d", tmdbID), nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (c *Client) GetTVCredits(ctx context.Context, tmdbID int64) (cast string, err error) {
	var cr Credits
	if err = c.get(ctx, fmt.Sprintf("/tv/%d/credits", tmdbID), nil, &cr); err != nil {
		return "", err
	}
	names := []string{}
	for i, p := range cr.Cast {
		if i >= 5 {
			break
		}
		names = append(names, p.Name)
	}
	return strings.Join(names, ", "), nil
}

func (c *Client) GetEpisode(ctx context.Context, tmdbID int64, season, episode int) (*Episode, error) {
	var e Episode
	if err := c.get(ctx, fmt.Sprintf("/tv/%d/season/%d/episode/%d", tmdbID, season, episode), nil, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func PosterURL(path string) string {
	if path == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/w600_and_h900_bestv2" + path
}

func BackdropURL(path string) string {
	if path == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/w1280" + path
}

func StillURL(path string) string {
	if path == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/w300" + path
}

func GenreNames(genres []Genre) []string {
	out := make([]string, len(genres))
	for i, g := range genres {
		out[i] = g.Name
	}
	return out
}
