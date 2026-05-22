// Package parser detects content type from a filename.
//
// Patterns:
//
//	240022.mp4              → movie  (TMDB ID 240022)
//	240022_S01E02.mp4       → series episode (TMDB ID 240022, season 1, episode 2)
//	240022 S01E02.mkv       → idem (espaço)
//	1399_S01E01_Pilot.mkv   → idem (sufixo extra ignorado)
package parser

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Kind int

const (
	KindUnknown Kind = iota
	KindMovie
	KindEpisode
)

type Result struct {
	Kind    Kind
	TMDBID  int64
	Season  int
	Episode int
}

var (
	episodeRe = regexp.MustCompile(`(?i)^(\d{1,9})[ _\-\.]*S(\d{1,3})E(\d{1,4})`)
	movieRe   = regexp.MustCompile(`^(\d{1,9})(?:[ _\-\.\(]|$)`)
)

func Parse(filename string) Result {
	name := filepath.Base(filename)
	// remove extension(s)
	for {
		ext := filepath.Ext(name)
		if ext == "" {
			break
		}
		// stop at non-typical media exts (avoid eating numbers)
		if !knownExt(strings.ToLower(ext)) {
			break
		}
		name = strings.TrimSuffix(name, ext)
	}
	name = strings.TrimSpace(name)

	if m := episodeRe.FindStringSubmatch(name); m != nil {
		id, _ := strconv.ParseInt(m[1], 10, 64)
		season, _ := strconv.Atoi(m[2])
		ep, _ := strconv.Atoi(m[3])
		return Result{Kind: KindEpisode, TMDBID: id, Season: season, Episode: ep}
	}
	if m := movieRe.FindStringSubmatch(name); m != nil {
		id, _ := strconv.ParseInt(m[1], 10, 64)
		return Result{Kind: KindMovie, TMDBID: id}
	}
	return Result{Kind: KindUnknown}
}

func knownExt(ext string) bool {
	switch ext {
	case ".mp4", ".mkv", ".avi", ".mov", ".webm", ".m4v", ".ts", ".flv", ".wmv",
		".mp3", ".aac", ".ogg", ".wav", ".flac", ".opus", ".m4a":
		return true
	}
	return false
}
