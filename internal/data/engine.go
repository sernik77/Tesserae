package data

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/silverx/tesserae/internal/config"
)

const defaultBackfill = 300

// Engine runs all sources of a dashboard and feeds the store.
type Engine struct {
	Store *Store
	Shell string

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewEngine returns an engine writing into store.
func NewEngine(store *Store, shell string) *Engine {
	return &Engine{Store: store, Shell: shell}
}

// ShellPath returns the shell used for commands: the configured one, else
// bash, else sh.
func ShellPath(configured string) string {
	if configured != "" {
		return configured
	}
	if p, err := exec.LookPath("bash"); err == nil {
		return p
	}
	return "sh"
}

// shellCmd prepares a shell command in its own process group so that
// cancelling it also kills its children (sleep, tail -f, journalctl…), which
// would otherwise keep the output pipe open.
func shellCmd(ctx context.Context, shell, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, ShellPath(shell), "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
	return cmd
}

// Start launches one goroutine per source. Invalid sources report their
// error through <name>.status / <name>.error instead of failing the whole run.
func (e *Engine) Start(sources []config.Source) {
	e.Stop()
	e.mu.Lock()
	defer e.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	for _, src := range sources {
		p, err := NewProcessor(src, e.Store)
		if err != nil {
			e.status(src.Name, err)
			continue
		}
		e.Store.Set(src.Name+".status", "waiting")
		e.wg.Add(1)
		go func(src config.Source) {
			defer e.wg.Done()
			r := &runner{e: e, src: src, p: p}
			r.run(ctx)
		}(src)
	}
}

// Stop cancels all sources and waits for them to exit.
func (e *Engine) Stop() {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	e.mu.Unlock()
	e.wg.Wait()
}

func (e *Engine) status(name string, err error) {
	if err != nil {
		e.Store.Set(name+".status", "error")
		e.Store.Set(name+".error", firstLine(err.Error()))
		return
	}
	if v, _ := e.Store.Get(name + ".status"); v != "ok" {
		e.Store.Set(name+".status", "ok")
		e.Store.Set(name+".error", "")
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

type runner struct {
	e   *Engine
	src config.Source
	p   *Processor
}

func (r *runner) run(ctx context.Context) {
	switch r.src.Type {
	case "tail":
		r.tail(ctx)
	case "exec":
		r.poll(ctx, r.exec)
	case "http":
		r.poll(ctx, r.http)
	case "stream":
		r.stream(ctx)
	default:
		r.e.status(r.src.Name, fmt.Errorf("unknown source type %q", r.src.Type))
	}
}

func (r *runner) counted() {
	r.e.Store.Set(r.src.Name+".lines", strconv.Itoa(r.p.lines))
}

// poll calls fetch immediately and then on every interval. Runs never overlap.
func (r *runner) poll(ctx context.Context, fetch func(context.Context) (string, error)) {
	iv := r.src.Interval.Duration
	if iv <= 0 {
		iv = time.Second
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		out, err := fetch(ctx)
		if ctx.Err() != nil {
			return
		}
		// Keep the last good values on failure: only process real output.
		if strings.TrimSpace(out) != "" {
			r.p.Whole(out)
			r.counted()
		}
		r.e.status(r.src.Name, err)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (r *runner) timeout(def time.Duration) time.Duration {
	if r.src.Timeout.Duration > 0 {
		return r.src.Timeout.Duration
	}
	return def
}

func (r *runner) exec(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout(max(10*time.Second, 3*r.src.Interval.Duration)))
	defer cancel()
	cmd := shellCmd(ctx, r.e.Shell, r.src.Command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		err = errors.New(msg)
	}
	return stdout.String(), err
}

func (r *runner) http(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout(5*time.Second))
	defer cancel()
	method := r.src.Method
	if method == "" {
		method = http.MethodGet
		if r.src.Body != "" {
			method = http.MethodPost
		}
	}
	var body io.Reader
	if r.src.Body != "" {
		body = strings.NewReader(r.src.Body)
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), r.src.URL, body)
	if err != nil {
		return "", err
	}
	for k, v := range r.src.Headers {
		req.Header.Set(k, v)
	}
	if r.src.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	r.e.Store.Set(r.src.Name+".code", strconv.Itoa(resp.StatusCode))
	if err == nil && resp.StatusCode >= 400 {
		err = fmt.Errorf("HTTP %s", resp.Status)
	}
	return string(data), err
}

// stream runs a long-lived command and processes its stdout line by line,
// restarting it with a back-off when it exits.
func (r *runner) stream(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		cmd := shellCmd(ctx, r.e.Shell, r.src.Command)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			r.e.status(r.src.Name, err)
		} else {
			started := time.Now()
			sc := bufio.NewScanner(out)
			sc.Buffer(make([]byte, 64<<10), 4<<20)
			for sc.Scan() {
				r.p.Line(sc.Text(), false)
				r.counted()
				r.e.status(r.src.Name, nil)
			}
			err = cmd.Wait()
			if ctx.Err() != nil {
				return
			}
			msg := strings.TrimSpace(stderr.String())
			if msg == "" && err != nil {
				msg = err.Error()
			}
			if msg == "" {
				msg = "command exited"
			}
			r.e.status(r.src.Name, errors.New(msg))
			if time.Since(started) > 30*time.Second {
				backoff = time.Second
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// tail follows a file like `tail -F`: survives rotation, truncation and the
// file not existing yet. Existing content is backfilled first.
func (r *runner) tail(ctx context.Context) {
	path := expandHome(r.src.Path)
	first := true
	for ctx.Err() == nil {
		f, err := os.Open(path)
		if err != nil {
			r.e.status(r.src.Name, err)
			if !sleep(ctx, time.Second) {
				return
			}
			continue
		}
		info, _ := f.Stat()
		offset := info.Size()
		if first {
			n := r.src.Backfill
			if n == 0 {
				n = defaultBackfill
			}
			if n > 0 {
				for _, l := range LastLines(f, offset, n) {
					if ctx.Err() != nil {
						f.Close()
						return
					}
					r.p.Line(l, true)
				}
				r.counted()
			}
			first = false
		} else {
			// The file was rotated or created: read it from the beginning.
			offset = 0
		}
		r.e.status(r.src.Name, nil)
		r.follow(ctx, f, offset, path)
		f.Close()
	}
}

// follow reads appended lines until the file is rotated or truncated.
func (r *runner) follow(ctx context.Context, f *os.File, offset int64, path string) {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return
	}
	rd := bufio.NewReaderSize(f, 64<<10)
	var partial []byte
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	for {
		read := false
		for {
			chunk, err := rd.ReadBytes('\n')
			if len(chunk) > 0 {
				offset += int64(len(chunk))
				if chunk[len(chunk)-1] == '\n' {
					line := append(partial, chunk[:len(chunk)-1]...)
					partial = nil
					r.p.Line(string(line), false)
					read = true
				} else {
					partial = append(partial, chunk...)
				}
			}
			if err != nil {
				break
			}
		}
		if read {
			r.counted()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur, err := f.Stat()
		now, err2 := os.Stat(path)
		if err != nil || err2 != nil || !os.SameFile(cur, now) || now.Size() < offset {
			return
		}
	}
}

// LastLines returns up to n complete lines before offset, reading backwards
// at most 16 MiB.
func LastLines(f *os.File, offset int64, n int) []string {
	const chunk = 64 << 10
	var buf []byte
	pos := offset
	for pos > 0 && bytes.Count(buf, []byte{'\n'}) <= n && int64(len(buf)) < 16<<20 {
		size := int64(chunk)
		if pos < size {
			size = pos
		}
		pos -= size
		b := make([]byte, size)
		if _, err := f.ReadAt(b, pos); err != nil && err != io.EOF {
			break
		}
		buf = append(b, buf...)
	}
	if pos > 0 {
		// Drop the first, probably incomplete line.
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	text := strings.TrimRight(string(buf), "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return h + p[1:]
		}
	}
	return os.ExpandEnv(p)
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// RunCommand runs a one-off shell command (button actions) and returns its
// combined output.
func RunCommand(ctx context.Context, shell, command string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := shellCmd(ctx, shell, command)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
