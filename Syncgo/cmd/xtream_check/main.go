// xtream_check: verifica fetch da API Xtream Codes sem precisar de MySQL.
// Uso: xtream_check.exe -url "http://server/get.php?username=X&password=Y&..."
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type vodStream struct {
	StreamID   json.Number `json:"stream_id"`
	Name       string      `json:"name"`
	StreamIcon string      `json:"stream_icon"`
	Genre      string      `json:"genre"`
	ReleaseDate string     `json:"release_date"`
	Rating     json.Number `json:"rating"`
}

type seriesEntry struct {
	SeriesID json.Number `json:"series_id"`
	Name     string      `json:"name"`
	Cover    string      `json:"cover"`
	Genre    string      `json:"genre"`
	Rating   json.Number `json:"rating"`
}

func main() {
	rawURL := flag.String("url", "", "URL get.php do Xtream (obrigatório)")
	maxMovies := flag.Int("movies", 5, "quantos filmes mostrar no preview (0 = só contar)")
	maxSeries := flag.Int("series", 5, "quantos séries mostrar no preview (0 = só contar)")
	flag.Parse()

	if *rawURL == "" {
		fmt.Fprintln(os.Stderr, "uso: xtream_check.exe -url \"http://server/get.php?username=X&password=Y&type=m3u_plus\"")
		os.Exit(1)
	}

	u, err := url.Parse(*rawURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "URL inválida: %v\n", err)
		os.Exit(1)
	}
	if !strings.Contains(u.Path, "get.php") {
		fmt.Fprintln(os.Stderr, "URL deve conter get.php")
		os.Exit(1)
	}
	q := u.Query()
	username := q.Get("username")
	password := q.Get("password")
	baseURL := fmt.Sprintf("%s://%s", u.Scheme, u.Host)

	client := &http.Client{Timeout: 120 * time.Second}
	ctx := context.Background()

	apiBase := func(action string) string {
		return fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=%s",
			baseURL, url.QueryEscape(username), url.QueryEscape(password), action)
	}

	fetch := func(endpoint string, out any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := client.Do(req)
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

	// ── Filmes ────────────────────────────────────────────────────────────────
	fmt.Printf("\n📡 Conectando em %s como %s ...\n\n", baseURL, username)
	fmt.Print("🎬 Buscando lista de filmes... ")

	var movies []vodStream
	t0 := time.Now()
	if err := fetch(apiBase("get_vod_streams"), &movies); err != nil {
		fmt.Fprintf(os.Stderr, "ERRO ao buscar filmes: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK (%d filmes em %s)\n", len(movies), time.Since(t0).Round(time.Millisecond))

	if *maxMovies > 0 {
		n := *maxMovies
		if n > len(movies) {
			n = len(movies)
		}
		fmt.Printf("\n  Primeiros %d filmes:\n", n)
		for i, m := range movies[:n] {
			icon := m.StreamIcon
			if len(icon) > 60 {
				icon = icon[:60] + "..."
			}
			fmt.Printf("  [%d] id=%-8s  %s\n", i+1, m.StreamID, m.Name)
			fmt.Printf("       genre=%-20s  date=%s  rating=%s\n", m.Genre, m.ReleaseDate, m.Rating)
			fmt.Printf("       icon=%s\n\n", icon)
		}
	}

	// ── Séries ────────────────────────────────────────────────────────────────
	fmt.Print("📺 Buscando lista de séries... ")

	var series []seriesEntry
	t0 = time.Now()
	if err := fetch(apiBase("get_series"), &series); err != nil {
		fmt.Fprintf(os.Stderr, "ERRO ao buscar séries: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK (%d séries em %s)\n", len(series), time.Since(t0).Round(time.Millisecond))

	if *maxSeries > 0 {
		n := *maxSeries
		if n > len(series) {
			n = len(series)
		}
		fmt.Printf("\n  Primeiras %d séries:\n", n)
		for i, s := range series[:n] {
			cover := s.Cover
			if len(cover) > 60 {
				cover = cover[:60] + "..."
			}
			fmt.Printf("  [%d] id=%-8s  %s\n", i+1, s.SeriesID, s.Name)
			fmt.Printf("       genre=%-20s  rating=%s\n", s.Genre, s.Rating)
			fmt.Printf("       cover=%s\n\n", cover)
		}
	}

	// ── Episódios de exemplo ──────────────────────────────────────────────────
	if len(series) > 0 && *maxSeries > 0 {
		first := series[0]
		fmt.Printf("🔍 Buscando episódios de \"%s\" (id=%s)... ", first.Name, first.SeriesID)

		type epInfo struct {
			EpisodeNum json.Number `json:"episode_num"`
			Title      string      `json:"title"`
			Season     json.Number `json:"season"`
		}
		type seriesInfoResp struct {
			Episodes map[string][]epInfo `json:"episodes"`
		}

		var info seriesInfoResp
		epURL := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_series_info&series_id=%s",
			baseURL, url.QueryEscape(username), url.QueryEscape(password), first.SeriesID)
		t0 = time.Now()
		if err := fetch(epURL, &info); err != nil {
			fmt.Printf("ERRO: %v\n", err)
		} else {
			total := 0
			for _, eps := range info.Episodes {
				total += len(eps)
			}
			fmt.Printf("OK (%d temporadas, %d episódios em %s)\n",
				len(info.Episodes), total, time.Since(t0).Round(time.Millisecond))

			// mostra ep 1 da temporada 1
			if eps, ok := info.Episodes["1"]; ok && len(eps) > 0 {
				e := eps[0]
				s, _ := e.Season.Int64()
				ep, _ := e.EpisodeNum.Int64()
				fmt.Printf("   Ex: S%02dE%02d — %s\n", s, ep, e.Title)
			}
		}
	}

	fmt.Printf("\n✅ Download OK — %d filmes, %d séries disponíveis\n\n", len(movies), len(series))
}
