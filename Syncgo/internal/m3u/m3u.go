// Package m3u parses M3U/M3U8 playlists.
package m3u

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strings"
)

type Entry struct {
	Name     string
	TVGID    string
	TVGName  string
	Logo     string
	Category string // group-title
	URL      string
}

var (
	tvgNameRe  = regexp.MustCompile(`tvg-name="([^"]*)"`)
	tvgIDRe    = regexp.MustCompile(`tvg-id="([^"]*)"`)
	logoRe     = regexp.MustCompile(`tvg-logo="([^"]*)"`)
	groupRe    = regexp.MustCompile(`group-title="([^"]*)"`)
	commaName  = regexp.MustCompile(`,\s*(.+)$`)
)

func ParseFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

func Parse(r io.Reader) ([]Entry, error) {
	var out []Entry
	var current Entry
	hasInfo := false

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4*1024*1024), 64*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line == "#EXTM3U" {
			continue
		}
		if strings.HasPrefix(line, "#EXTINF") {
			current = Entry{}
			if m := tvgNameRe.FindStringSubmatch(line); m != nil {
				current.TVGName = m[1]
				current.Name = m[1]
			}
			if m := tvgIDRe.FindStringSubmatch(line); m != nil {
				current.TVGID = m[1]
			}
			if m := logoRe.FindStringSubmatch(line); m != nil {
				current.Logo = m[1]
			}
			if m := groupRe.FindStringSubmatch(line); m != nil {
				current.Category = m[1]
			}
			if current.Name == "" {
				if m := commaName.FindStringSubmatch(line); m != nil {
					current.Name = strings.TrimSpace(m[1])
				}
			}
			hasInfo = true
		} else if strings.HasPrefix(line, "#") {
			continue
		} else if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") || strings.HasPrefix(line, "rtmp://") {
			if hasInfo {
				current.URL = line
				if current.Name != "" && current.URL != "" {
					out = append(out, current)
				}
				hasInfo = false
				current = Entry{}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
