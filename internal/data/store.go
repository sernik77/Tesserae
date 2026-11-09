// Package data turns raw text from sources into variables and event streams.
package data

import (
	"maps"
	"os"
	"os/user"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sernik77/tesserae/internal/tmpl"
)

const (
	historyCap = 600
	streamCap  = 1000 // event streams
	sourceCap  = 5000 // raw source lines (also used by the rule tester)
)

// Var is the current value of a variable.
type Var struct {
	Val string
	At  time.Time
}

// Entry is one line of an event stream. Text is markup.
type Entry struct {
	At   time.Time // zero for backfilled entries
	Text string
}

// Store holds all variables, their numeric history and event streams.
// It is safe for concurrent use.
type Store struct {
	mu      sync.RWMutex
	vars    map[string]Var
	hist    map[string][]float64
	streams map[string][]Entry
	version atomic.Uint64
	start   time.Time
	host    string
	user    string
}

// NewStore returns an empty store.
func NewStore() *Store {
	s := &Store{
		vars:    map[string]Var{},
		hist:    map[string][]float64{},
		streams: map[string][]Entry{},
		start:   time.Now(),
	}
	s.host, _ = os.Hostname()
	if u, err := user.Current(); err == nil {
		s.user = u.Username
	}
	return s
}

// Version increases on every change; cheap change detection for renderers.
func (s *Store) Version() uint64 { return s.version.Load() }

// Set assigns a variable.
func (s *Store) Set(name, val string) {
	s.mu.Lock()
	s.vars[name] = Var{Val: val, At: time.Now()}
	s.mu.Unlock()
	s.version.Add(1)
}

// SetDefault assigns a variable only when it does not exist yet.
func (s *Store) SetDefault(name, val string) {
	s.mu.Lock()
	if _, ok := s.vars[name]; !ok {
		s.vars[name] = Var{Val: val}
	}
	s.mu.Unlock()
	s.version.Add(1)
}

// Delete removes variables.
func (s *Store) Delete(names ...string) {
	s.mu.Lock()
	for _, n := range names {
		delete(s.vars, n)
		delete(s.hist, n)
	}
	s.mu.Unlock()
	s.version.Add(1)
}

// Inc increments a numeric variable by one.
func (s *Store) Inc(name string) {
	s.mu.Lock()
	n, _ := tmpl.ParseNum(s.vars[name].Val)
	s.vars[name] = Var{Val: strconv.FormatFloat(n+1, 'f', -1, 64), At: time.Now()}
	s.mu.Unlock()
	s.version.Add(1)
}

// Get implements tmpl.Lookup. Names starting with "sys." are built-ins.
func (s *Store) Get(name string) (string, bool) {
	if strings.HasPrefix(name, "sys.") {
		if v, ok := s.builtin(name[4:]); ok {
			return v, true
		}
	}
	s.mu.RLock()
	v, ok := s.vars[name]
	s.mu.RUnlock()
	return v.Val, ok
}

// Lookup returns the full variable record.
func (s *Store) Lookup(name string) (Var, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.vars[name]
	return v, ok
}

func (s *Store) builtin(name string) (string, bool) {
	now := time.Now()
	switch name {
	case "time":
		return now.Format("15:04:05"), true
	case "date":
		return now.Format("2006-01-02"), true
	case "datetime":
		return now.Format("2006-01-02 15:04:05"), true
	case "epoch":
		return strconv.FormatInt(now.Unix(), 10), true
	case "uptime":
		d := int(now.Sub(s.start).Seconds())
		return pad2(d/3600) + ":" + pad2(d%3600/60) + ":" + pad2(d%60), true
	case "uptime_s":
		return strconv.Itoa(int(now.Sub(s.start).Seconds())), true
	case "host":
		return s.host, true
	case "user":
		return s.user, true
	}
	return "", false
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// History implements tmpl.Lookup: sampled numeric values, oldest first.
func (s *Store) History(name string) []float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.hist[name])
}

// Sample appends the current value of every numeric variable to its history.
// Called once per refresh tick so that history has an even time axis.
func (s *Store) Sample() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, v := range s.vars {
		f, ok := tmpl.ParseNum(v.Val)
		if !ok {
			continue
		}
		h := append(s.hist[name], f)
		if len(h) > historyCap {
			h = h[len(h)-historyCap:]
		}
		s.hist[name] = h
	}
}

// Push appends an entry to an event stream.
func (s *Store) Push(stream, text string, at time.Time) {
	s.mu.Lock()
	e := append(s.streams[stream], Entry{At: at, Text: text})
	limit := streamCap
	if strings.HasPrefix(stream, "source:") {
		limit = sourceCap
	}
	if len(e) > limit {
		e = slices.Clone(e[len(e)-limit*3/4:])
	}
	s.streams[stream] = e
	s.mu.Unlock()
	s.version.Add(1)
}

// Replace sets the whole content of a stream.
func (s *Store) Replace(stream string, entries []Entry) {
	s.mu.Lock()
	s.streams[stream] = entries
	s.mu.Unlock()
	s.version.Add(1)
}

// Stream returns up to the last n entries of a stream (n <= 0: all).
func (s *Store) Stream(name string, n int) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e := s.streams[name]
	if n > 0 && len(e) > n {
		e = e[len(e)-n:]
	}
	return slices.Clone(e)
}

// StreamNames lists all stream names, sorted.
func (s *Store) StreamNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Sorted(maps.Keys(s.streams))
}

// Names lists all variable names (including built-ins), sorted.
func (s *Store) Names() []string {
	s.mu.RLock()
	names := slices.Collect(maps.Keys(s.vars))
	s.mu.RUnlock()
	for _, b := range []string{"time", "date", "datetime", "epoch", "uptime", "uptime_s", "host", "user"} {
		names = append(names, "sys."+b)
	}
	slices.Sort(names)
	return names
}
