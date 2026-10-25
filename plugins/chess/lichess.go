package chess

// lichess.go — عميل Lichess Cloud Eval مع كاش LRU.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ─── Lichess Cloud Eval مع كاش LRU (يشمل غياب التحليل) ──────────────────────

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
