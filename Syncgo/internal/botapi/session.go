package botapi

import (
	"context"
	"fmt"
	"sync"
	"time"

	"syncgo/internal/parser"
	"syncgo/internal/xui"
)

type ctKind int

const (
	ctUnknown ctKind = iota
	ctMovie
	ctEpisode
)

func (k ctKind) label() string {
	switch k {
	case ctMovie:
		return "🎬 Filme"
	case ctEpisode:
		return "📺 Série"
	default:
		return "❓ Desconhecido"
	}
}

type sessState int

const (
	stConfirm  sessState = iota // mostrando mensagem de confirmação
	stEditTMDB                  // aguardando usuário digitar novo TMDB ID
	stEditSE                    // aguardando usuário digitar S01E02
)

type insertSession struct {
	Key          string
	FileName     string
	StreamURL    string
	ChatID       int64
	ReplyMsgID   int
	ConfirmMsgID int // ID da mensagem de confirmação (para editar)

	Kind    ctKind
	TMDBID  int64
	Season  int
	Episode int

	State   sessState
	Expires time.Time
}

func sessKey(chatID int64, msgID int) string {
	return fmt.Sprintf("%d:%d", chatID, msgID)
}

type parsedInfo struct {
	Kind    ctKind
	TMDBID  int64
	Season  int
	Episode int
}

func parseToSession(fileName string) parsedInfo {
	res := parser.Parse(fileName)
	switch res.Kind {
	case parser.KindMovie:
		return parsedInfo{Kind: ctMovie, TMDBID: res.TMDBID}
	case parser.KindEpisode:
		return parsedInfo{Kind: ctEpisode, TMDBID: res.TMDBID, Season: res.Season, Episode: res.Episode}
	default:
		return parsedInfo{Kind: ctUnknown}
	}
}

type sessionStore struct {
	mu sync.Mutex
	m  map[int64]*insertSession // chatID -> session ativa
}

func newSessionStore() *sessionStore {
	return &sessionStore{m: make(map[int64]*insertSession)}
}

// ── Setup wizard session ─────────────────────────────────────────────────────

type setupStage int

const (
	ssHost        setupStage = iota // digitar host
	ssPort                          // digitar porta
	ssUser                          // digitar usuário
	ssPass                          // digitar senha
	ssDbName                        // digitar banco
	ssMode                          // escolher modo (inline)
	ssMovieBq                       // escolher bouquet filmes (inline)
	ssNewMovieBq                    // digitar nome novo bouquet filmes
	ssSeriesBq                      // escolher bouquet séries (inline)
	ssNewSeriesBq                   // digitar nome novo bouquet séries
)

type setupSession struct {
	ChatID  int64
	Stage   setupStage
	Expires time.Time

	Host, Port, User, Pass, DbName string
	Mode                           string // "auto" | "manual"

	MovieBouquetID    int64
	MovieBouquetName  string
	SeriesBouquetID   int64
	SeriesBouquetName string

	XUIDB *xui.DB // conexão ativa durante o wizard (fechada ao terminar/cancelar)
}

func (s *setupSession) closeXUI() {
	if s.XUIDB != nil {
		_ = s.XUIDB.Close()
		s.XUIDB = nil
	}
}

// ── M3U add session ──────────────────────────────────────────────────────────

type m3uStage int

const (
	m3uStageURL  m3uStage = iota // aguardando usuário digitar a URL
	m3uStageName                  // aguardando usuário digitar o nome
)

type m3uAddSession struct {
	ChatID  int64
	Stage   m3uStage
	URL     string
	Expires time.Time
}

type m3uStore struct {
	mu sync.Mutex
	m  map[int64]*m3uAddSession
}

func newM3UStore() *m3uStore { return &m3uStore{m: make(map[int64]*m3uAddSession)} }

func (s *m3uStore) set(sess *m3uAddSession) {
	s.mu.Lock()
	s.m[sess.ChatID] = sess
	s.mu.Unlock()
}

func (s *m3uStore) get(chatID int64) (*m3uAddSession, bool) {
	s.mu.Lock()
	v, ok := s.m[chatID]
	s.mu.Unlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(v.Expires) {
		s.del(chatID)
		return nil, false
	}
	return v, true
}

func (s *m3uStore) del(chatID int64) {
	s.mu.Lock()
	delete(s.m, chatID)
	s.mu.Unlock()
}

type setupStore struct {
	mu sync.Mutex
	m  map[int64]*setupSession
}

func newSetupStore() *setupStore { return &setupStore{m: make(map[int64]*setupSession)} }

func (s *setupStore) set(sess *setupSession) {
	s.mu.Lock()
	s.m[sess.ChatID] = sess
	s.mu.Unlock()
}

func (s *setupStore) get(chatID int64) (*setupSession, bool) {
	s.mu.Lock()
	v, ok := s.m[chatID]
	s.mu.Unlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(v.Expires) {
		v.closeXUI()
		s.del(chatID)
		return nil, false
	}
	return v, true
}

func (s *setupStore) del(chatID int64) {
	s.mu.Lock()
	if v, ok := s.m[chatID]; ok {
		v.closeXUI()
	}
	delete(s.m, chatID)
	s.mu.Unlock()
}

func (s *sessionStore) set(sess *insertSession) {
	s.mu.Lock()
	s.m[sess.ChatID] = sess
	s.mu.Unlock()
}

func (s *sessionStore) get(chatID int64) (*insertSession, bool) {
	s.mu.Lock()
	v, ok := s.m[chatID]
	s.mu.Unlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(v.Expires) {
		s.del(chatID)
		return nil, false
	}
	return v, true
}

func (s *sessionStore) del(chatID int64) {
	s.mu.Lock()
	delete(s.m, chatID)
	s.mu.Unlock()
}

// ── Xtream background job tracking ──────────────────────────────────────────

type xtreamJob struct {
	ChatID     int64
	NotifMsgID int
	Kind       string // "movies" | "series" | "all"
	Cancel     context.CancelFunc
}

type xtreamJobStore struct {
	mu sync.Mutex
	m  map[int64]*xtreamJob // sourceID → job
}

func newXtreamJobStore() *xtreamJobStore {
	return &xtreamJobStore{m: make(map[int64]*xtreamJob)}
}

func (s *xtreamJobStore) set(sourceID int64, j *xtreamJob) {
	s.mu.Lock()
	s.m[sourceID] = j
	s.mu.Unlock()
}

func (s *xtreamJobStore) get(sourceID int64) (*xtreamJob, bool) {
	s.mu.Lock()
	v, ok := s.m[sourceID]
	s.mu.Unlock()
	return v, ok
}

func (s *xtreamJobStore) del(sourceID int64) {
	s.mu.Lock()
	delete(s.m, sourceID)
	s.mu.Unlock()
}
