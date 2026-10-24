package chess

// engine.go — مجمّع عمليات Stockfish الدائمة (UCI) + Lichess Cloud Eval.
//
// عمليات Stockfish تبقى حيّة وتُعاد (لا fork/exec ولا تحميل NNUE لكل نقلة)،
// وتُنشأ عند الحاجة حتى حدّ أقصى فلا تُستهلك ذاكرة بلا داعٍ.

import (
	"bufio"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ─── عملية UCI واحدة ────────────────────────────────────────────────────────

type uciEngine struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	lines chan string
	done  chan struct{} // يُغلق عند انتهاء العملية
}

func startUCI(path string, options map[string]string) (*uciEngine, error) {
	cmd := exec.Command(path)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	e := &uciEngine{cmd: cmd, in: in, lines: make(chan string, 256), done: make(chan struct{})}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			select {
			case e.lines <- sc.Text():
			default: // لا نحجب القارئ إن امتلأت القناة (أسطر info كثيرة)
				select {
				case <-e.lines:
				default:
				}
				e.lines <- sc.Text()
			}
		}
		close(e.done)
		_ = cmd.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.send("uci"); err != nil {
		e.kill()
		return nil, err
	}
	supported := map[string]bool{}
	if err := e.waitFor(ctx, "uciok", func(l string) {
		if strings.HasPrefix(l, "option name ") {
			rest := strings.TrimPrefix(l, "option name ")
			if i := strings.Index(rest, " type "); i > 0 {
				supported[rest[:i]] = true
			}
		}
	}); err != nil {
		e.kill()
		return nil, err
	}
	for k, v := range options {
		if supported[k] {
			_ = e.send(fmt.Sprintf("setoption name %s value %s", k, v))
		}
	}
	if err := e.ready(ctx); err != nil {
		e.kill()
		return nil, err
	}
	return e, nil
}

func (e *uciEngine) send(cmd string) error {
	_, err := io.WriteString(e.in, cmd+"\n")
	return err
}

// waitFor يقرأ الأسطر حتى يجد سطراً يبدأ بـ prefix.
func (e *uciEngine) waitFor(ctx context.Context, prefix string, each func(string)) error {
	for {
		select {
		case l := <-e.lines:
			if each != nil {
				each(l)
			}
			if strings.HasPrefix(l, prefix) {
				return nil
			}
		case <-e.done:
			return errors.New("انتهت عملية المحرك")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (e *uciEngine) ready(ctx context.Context) error {
	if err := e.send("isready"); err != nil {
		return err
	}
	return e.waitFor(ctx, "readyok", nil)
}

func (e *uciEngine) kill() {
	_ = e.in.Close()
	if e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}
}

// bestMove يبحث في الموضع ويعيد نقلة UCI.
func (e *uciEngine) bestMove(ctx context.Context, fen string, depth int, movetime time.Duration) (string, error) {
	// نفرّغ أي بقايا من بحث سابق
	for drained := false; !drained; {
		select {
		case <-e.lines:
		default:
			drained = true
		}
	}
	if err := e.send("position fen " + fen); err != nil {
		return "", err
	}
	if err := e.send(fmt.Sprintf("go depth %d movetime %d", depth, movetime.Milliseconds())); err != nil {
		return "", err
	}
	var best string
	err := e.waitFor(ctx, "bestmove", func(l string) {
		if strings.HasPrefix(l, "bestmove") {
			f := strings.Fields(l)
			if len(f) >= 2 && f[1] != "(none)" && f[1] != "0000" {
				best = f[1]
			}
		}
	})
	if err != nil {
		_ = e.send("stop")
		return "", err
	}
	return best, nil
}

// ─── المجمّع ────────────────────────────────────────────────────────────────

type enginePool struct {
	path    string
	size    int
	options map[string]string

	mu       sync.Mutex
	created  int
	idle     chan *uciEngine
	failedAt time.Time
	lastErr  string
	closed   bool
}

const retryAfterFailure = 60 * time.Second

func newEnginePool(path string, size int, options map[string]string) *enginePool {
	if size < 1 {
		size = 1
	}
	return &enginePool{path: path, size: size, options: options, idle: make(chan *uciEngine, size)}
}

func (p *enginePool) available() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	return p.failedAt.IsZero() || time.Since(p.failedAt) > retryAfterFailure
}

func (p *enginePool) info() (running, idle int, lastErr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.created, len(p.idle), p.lastErr
}

func (p *enginePool) checkout(ctx context.Context) (*uciEngine, error) {
	select {
	case e := <-p.idle:
		return e, nil
	default:
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("المجمّع مغلق")
	}
	if p.created < p.size {
		p.created++
		p.mu.Unlock()
		e, err := startUCI(p.path, p.options)
		if err != nil {
			p.mu.Lock()
			p.created--
			p.failedAt = time.Now()
			p.lastErr = err.Error()
			p.mu.Unlock()
			return nil, err
		}
		p.mu.Lock()
		p.failedAt = time.Time{}
		p.mu.Unlock()
		return e, nil
	}
	p.mu.Unlock()
	select {
	case e := <-p.idle:
		return e, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *enginePool) checkin(e *uciEngine, healthy bool) {
	if healthy {
		select {
		case p.idle <- e:
			return
		default:
		}
	}
	e.kill()
	p.mu.Lock()
	p.created--
	p.mu.Unlock()
}

// play يعيد نقلة UCI من المحرك (يعيد المحاولة مرة بمحرك جديد إن مات الأول).
func (p *enginePool) play(ctx context.Context, fen string, depth int, movetime time.Duration) (string, error) {
	if !p.available() {
		return "", errors.New("المحرك غير متاح")
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		e, err := p.checkout(ctx)
		if err != nil {
			return "", err
		}
		sctx, cancel := context.WithTimeout(ctx, movetime+10*time.Second)
		mv, err := e.bestMove(sctx, fen, depth, movetime)
		cancel()
		p.checkin(e, err == nil)
		if err == nil {
			return mv, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return "", lastErr
}

// warm يُشغّل محركاً ويُجري بحثاً خفيفاً لتحميل الشبكة مسبقاً.
func (p *enginePool) warm() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := p.play(ctx, "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 1, 200*time.Millisecond)
	return err
}

func (p *enginePool) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	for {
		select {
		case e := <-p.idle:
			e.kill()
		default:
			return
		}
	}
}

// ─── Lichess Cloud Eval مع كاش LRU (يشمل غياب التحليل) ──────────────────────

type lruCache struct {
	mu  sync.Mutex
	max int
	ll  *list.List
	m   map[string]*list.Element
}

type lruEntry struct {
	key string
	val string
}

func newLRU(max int) *lruCache {
	return &lruCache{max: max, ll: list.New(), m: make(map[string]*list.Element)}
}

func (c *lruCache) get(k string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.m[k]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*lruEntry).val, true
	}
	return "", false
}

func (c *lruCache) put(k, v string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.m[k]; ok {
		el.Value.(*lruEntry).val = v
		c.ll.MoveToFront(el)
		return
	}
	c.m[k] = c.ll.PushFront(&lruEntry{k, v})
	for c.ll.Len() > c.max {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.m, last.Value.(*lruEntry).key)
	}
}

type lichessClient struct {
	http  *http.Client
	url   string
	token string
	cache *lruCache
}

func newLichess(token string, timeout time.Duration) *lichessClient {
	return &lichessClient{
		http:  &http.Client{Timeout: timeout},
		url:   "https://lichess.org/api/cloud-eval",
		token: token,
		cache: newLRU(4096),
	}
}

// posKey: أول 4 حقول من FEN (بدون عدّادات النقلات) لرفع نسبة الإصابة.
func posKey(fen string) string {
	f := strings.Fields(fen)
	if len(f) > 4 {
		f = f[:4]
	}
	return strings.Join(f, " ")
}

// move يعيد نقلة Lichess أو "" إن لم يتوفر تحليل / فشل الطلب.
func (l *lichessClient) move(ctx context.Context, fen string) string {
	key := posKey(fen)
	if v, ok := l.cache.get(key); ok {
		return v
	}
	q := url.Values{"fen": {fen}, "multiPv": {"1"}, "variant": {"standard"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.url+"?"+q.Encode(), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")
	if l.token != "" {
		req.Header.Set("Authorization", "Bearer "+l.token)
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		var body struct {
			Pvs []struct {
				Moves string `json:"moves"`
			} `json:"pvs"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) == nil && len(body.Pvs) > 0 {
			if f := strings.Fields(body.Pvs[0].Moves); len(f) > 0 {
				l.cache.put(key, f[0])
				return f[0]
			}
		}
	case resp.StatusCode == http.StatusNotFound:
		l.cache.put(key, "") // لا يوجد تحليل — تذكّر ذلك
	}
	return ""
}

func envInt(get func(string) string, key string, def int) int {
	if v := get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
