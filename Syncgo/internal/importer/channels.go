package importer

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"syncgo/internal/m3u"
	"syncgo/internal/xui"
)

type ChannelImporter struct {
	xui      *xui.DB
	reloader *xui.Reloader
	log      *slog.Logger
	http     *http.Client
	groups   map[string][]string // category → keyword patterns (pt-BR brand mapping)
}

func (c *ChannelImporter) Close() {
	if c.xui != nil {
		_ = c.xui.Close()
		c.xui = nil
	}
}

func NewChannelImporter(x *xui.DB, reloader *xui.Reloader, log *slog.Logger) *ChannelImporter {
	return &ChannelImporter{
		xui:      x,
		reloader: reloader,
		log:      log,
		http:     &http.Client{Timeout: 120 * time.Second},
		groups: map[string][]string{
			"CANAIS GLOBO":                  {"globo", "globoplay", "globo play", "rede globo"},
			"CANAIS SBT":                    {"sbt"},
			"CANAIS RECORD":                 {"record"},
			"CANAIS BAND":                   {"band"},
			"CANAIS TELECINE":               {"telecine"},
			"CANAIS HBO":                    {"hbo"},
			"CANAIS DE TV ABERTA":           {"redetv", "rede tv", "tv cultura", "tv brasil", "futura", "rede gazeta"},
			"CANAIS DE ESPORTES":            {"sport tv", "espn", "fox sports", "band sports", "premiere"},
			"CANAIS DE FILMES E SÉRIES":     {"warner tv", "universal tv", "starz", "studio universal", "paramount", "amc", "tnt", "fx"},
			"CANAIS INFANTIS":               {"cartoon", "nick", "babytv", "tooncast", "discovery kids", "gloob"},
			"CANAIS DE DOCUMENTÁRIOS":       {"discovery", "national geographic", "history", "investigation", "tlc", "science"},
			"CANAIS DE LIFESTYLE/CULINÁRIA": {"food network", "hgtv", "off"},
			"CANAIS RELIGIOSOS":             {"canção nova", "rede vida", "aparecida", "lbv", "viva"},
			"CANAIS DE NOTÍCIAS":            {"cnn", "globonews", "bandnews", "fox news", "msnbc", "record news"},
			"CANAIS DE MÚSICA":              {"mtv", "music"},
		},
	}
}

type ChannelImportResult struct {
	TotalRead    int
	Inserted     int
	Updated      int
	Categories   int
	BouquetID    int64
	StreamIDs    []int64
}

func (c *ChannelImporter) ImportFromFile(ctx context.Context, path string) (*ChannelImportResult, error) {
	entries, err := m3u.ParseFile(path)
	if err != nil {
		return nil, fmt.Errorf("parse m3u: %w", err)
	}
	c.log.Info("m3u parsed", "file", path, "entries", len(entries))
	return c.importEntries(ctx, entries)
}

func (c *ChannelImporter) ImportFromURL(ctx context.Context, rawURL string) (*ChannelImportResult, error) {
	delays := []time.Duration{3 * time.Second, 8 * time.Second, 20 * time.Second}
	var lastErr error
	for attempt := 0; attempt <= len(delays); attempt++ {
		if attempt > 0 {
			wait := delays[attempt-1]
			c.log.Info("m3u retry after 429", "attempt", attempt, "wait", wait)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("User-Agent", "VLC/3.0.20 LibVLC/3.0.20")
		req.Header.Set("Accept", "*/*")

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("fetch m3u: %w", err)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			// Respeita Retry-After se presente
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil && secs > 0 && secs < 120 {
					delays[min(attempt, len(delays)-1)] = time.Duration(secs) * time.Second
				}
			}
			lastErr = fmt.Errorf("fetch m3u: HTTP 429 (servidor limitando; tentativa %d/%d)", attempt+1, len(delays)+1)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("fetch m3u: HTTP %d", resp.StatusCode)
		}

		entries, err := m3u.Parse(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("parse m3u: %w", err)
		}
		c.log.Info("m3u fetched from url", "url", rawURL, "entries", len(entries))
		return c.importEntries(ctx, entries)
	}

	return nil, lastErr
}


func (c *ChannelImporter) importEntries(ctx context.Context, entries []m3u.Entry) (*ChannelImportResult, error) {
	if c.xui == nil {
		return nil, fmt.Errorf("XUI database não configurado")
	}
	res := &ChannelImportResult{TotalRead: len(entries)}

	// group multiple URLs by base name
	type bucket struct {
		urls     []string
		logo     string
		category string
	}
	grouped := map[string]*bucket{}
	for _, e := range entries {
		base := baseChannelName(e.Name)
		if _, ok := grouped[base]; !ok {
			grouped[base] = &bucket{}
		}
		b := grouped[base]
		b.urls = append(b.urls, e.URL)
		if b.logo == "" {
			b.logo = e.Logo
		}
		if b.category == "" {
			b.category = e.Category
		}
	}

	bouquetID, err := c.xui.GetOrCreateBouquet(ctx, "CANAIS")
	if err != nil {
		return nil, fmt.Errorf("bouquet CANAIS: %w", err)
	}
	res.BouquetID = bouquetID

	preCats, err := c.xui.PreloadCategories(ctx)
	if err != nil {
		c.log.Warn("preload categories", "err", err)
		preCats = map[string]int64{}
	}
	categoryCache := make(map[string]int64, len(preCats))
	for k, v := range preCats {
		categoryCache[k] = v
	}

	// Pré-carrega canais existentes para eliminar SELECT N+1 no loop.
	channelCache, err := c.xui.PreloadChannels(ctx)
	if err != nil {
		c.log.Warn("preload channels", "err", err)
		channelCache = map[string]int64{}
	}

	for base, b := range grouped {
		categoryName := b.category
		if categoryName == "" {
			categoryName = c.identifyCategory(base)
		}
		categoryName = strings.TrimSpace(categoryName)
		if categoryName == "" {
			categoryName = "Outros Canais"
		}
		cacheKey := "live|" + categoryName
		categoryID, ok := categoryCache[cacheKey]
		if !ok {
			id, err := c.xui.GetOrCreateCategory(ctx, categoryName, "live")
			if err != nil {
				c.log.Error("get/create category", "name", categoryName, "err", err)
				continue
			}
			categoryID = id
			categoryCache[cacheKey] = id
		}

		streamID, isNew, err := c.xui.UpsertChannelCached(ctx, xui.Channel{
			BaseName:    base,
			URLs:        b.urls,
			LogoURL:     b.logo,
			CategoryIDs: []int64{categoryID},
		}, channelCache)
		if err != nil {
			c.log.Error("upsert channel", "name", base, "err", err)
			continue
		}
		if isNew {
			res.Inserted++
			channelCache[base] = streamID // evita re-verificação se mesmo nome aparecer de novo
		} else {
			res.Updated++
		}
		res.StreamIDs = append(res.StreamIDs, streamID)
	}
	res.Categories = len(categoryCache)

	if len(res.StreamIDs) > 0 {
		if err := c.xui.AddToBouquet(ctx, bouquetID, xui.BouquetChannels, res.StreamIDs...); err != nil {
			c.log.Error("add to bouquet CANAIS", "err", err)
		}
	}
	if c.reloader != nil && (res.Inserted+res.Updated) > 0 {
		c.reloader.Trigger()
	}
	return res, nil
}

func (c *ChannelImporter) identifyCategory(name string) string {
	low := strings.ToLower(name)
	for cat, keywords := range c.groups {
		for _, k := range keywords {
			if strings.Contains(low, k) {
				return cat
			}
		}
	}
	return "Outros Canais"
}

var (
	canalNumRe = regexp.MustCompile(`(?i)\s*CANAL\s*\d+`)
	trailNum   = regexp.MustCompile(`\s*\d+\s*$`)
)

func baseChannelName(name string) string {
	low := strings.ToLower(name)
	if strings.Contains(low, "rede globo") || strings.Contains(low, "globo") {
		if strings.Contains(low, "globo play") || strings.Contains(low, "globoplay") {
			return "Globo Play"
		}
		return "Rede Globo"
	}
	out := canalNumRe.ReplaceAllString(name, "")
	out = trailNum.ReplaceAllString(out, "")
	return strings.TrimSpace(out)
}
