package parser

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Result
	}{
		{"movie simple", "240022.mp4", Result{Kind: KindMovie, TMDBID: 240022}},
		{"movie with title", "240022 - The Movie.mkv", Result{Kind: KindMovie, TMDBID: 240022}},
		{"episode underscore", "240022_S01E02.mp4", Result{Kind: KindEpisode, TMDBID: 240022, Season: 1, Episode: 2}},
		{"episode space", "240022 S01E02.mkv", Result{Kind: KindEpisode, TMDBID: 240022, Season: 1, Episode: 2}},
		{"episode with title suffix", "1399_S01E01_Pilot.mkv", Result{Kind: KindEpisode, TMDBID: 1399, Season: 1, Episode: 1}},
		{"non-numeric", "MyMovie.mp4", Result{Kind: KindUnknown}},
		{"path", "/foo/bar/240022_S03E04.mp4", Result{Kind: KindEpisode, TMDBID: 240022, Season: 3, Episode: 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.in)
			if got != tc.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}
