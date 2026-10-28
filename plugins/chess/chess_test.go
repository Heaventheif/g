package chess

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("LICHESS_TIMEOUT_MS", "50")
	s := newService()
	s.lichess.url = "http://127.0.0.1:1/unused" // لا شبكة في الاختبارات
	// لا تُترك عمليات Stockfish بعد الاختبار
	t.Cleanup(s.pool.close)
	return s
}

func post(t *testing.T, s *Service, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/process_move", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Routes()[0].Handler(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("رد غير JSON: %s", rec.Body.String())
	}
	return rec.Code, out
}

func TestProcessMovePlayerOnly(t *testing.T) {
	s := newTestService(t)
	code, out := post(t, s, `{"fen":"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1","move":"e2e4"}`)
	if code != 200 {
		t.Fatalf("code=%d %v", code, out)
	}
	if out["new_fen"] != "rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq - 0 1" {
		t.Errorf("new_fen = %v", out["new_fen"])
	}
	raw, err := base64.StdEncoding.DecodeString(out["image_base64"].(string))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil || img.Bounds().Dx() != 480 || img.Bounds().Dy() != 480 {
		t.Fatalf("PNG غير صالحة: %v %v", err, img.Bounds())
	}
	if out["game_over"] != false || out["winner"] != nil {
		t.Errorf("game_over/winner خاطئ: %v %v", out["game_over"], out["winner"])
	}
}

func TestProcessMoveErrors(t *testing.T) {
	s := newTestService(t)
	start := "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"
	code, out := post(t, s, `{"fen":"`+start+`","move":"e2e5"}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(out["detail"].(string), "غير قانونية") {
		t.Errorf("%v", out)
	}
	code, out = post(t, s, `{"fen":"`+start+`","move":"hello"}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(out["detail"].(string), "صيغة خاطئة") {
		t.Errorf("%v", out)
	}
	if code, _ := post(t, s, `{"fen":"garbage"}`); code != http.StatusBadRequest {
		t.Errorf("FEN سيئ: code=%d", code)
	}
	if code, out := post(t, s, `{}`); code != http.StatusUnprocessableEntity || out["detail"] != "fen مطلوب" {
		t.Errorf("fen مفقود: code=%d", code)
	}
	if code, out := post(t, s, `{bad`); code != http.StatusBadRequest || out["error"] != "invalid request body" {
		t.Errorf("JSON غير صالح: code=%d body=%v", code, out)
	}
}

func TestProcessMoveCheckmate(t *testing.T) {
	s := newTestService(t)
	_, out := post(t, s, `{"fen":"rnbqkbnr/pppp1ppp/8/4p3/6P1/5P2/PPPPP2P/RNBQKBNR b KQkq - 0 2","move":"d8h4","perspective":"black"}`)
	if out["game_over"] != true || out["winner"] != "أسود" {
		t.Errorf("mate: %v", out)
	}
}

// البوت بلا Stockfish ولا شبكة → نقلة عشوائية قانونية دائماً.
func TestBotFallbackAlwaysPlays(t *testing.T) {
	s := newTestService(t)
	s.pool = newEnginePool("/nonexistent/stockfish", 1, nil)
	_, out := post(t, s, `{"fen":"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1","bot_mode":true}`)
	fen := out["new_fen"].(string)
	if !strings.Contains(fen, " b ") || fen == "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1" {
		t.Errorf("البوت لم يلعب: %v", out)
	}
}

func TestBotWithStockfish(t *testing.T) {
	path, err := exec.LookPath("stockfish")
	if err != nil {
		if _, e2 := os.Stat("/usr/games/stockfish"); e2 != nil {
			t.Skip("stockfish غير مثبّت")
		}
		path = "/usr/games/stockfish"
	}
	s := newTestService(t)
	s.pool = newEnginePool(path, 2, map[string]string{"Hash": "16"})
	defer s.pool.close()
	if err := s.pool.warm(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ { // عمليات دائمة: عدة نقلات متتالية بنفس المحرك
		t0 := time.Now()
		_, out := post(t, s, `{"fen":"rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq - 0 1","bot_mode":true,"difficulty":8}`)
		if out["new_fen"] == nil || !strings.Contains(out["new_fen"].(string), " w ") {
			t.Fatalf("البوت لم يلعب: %v", out)
		}
		t.Logf("نقلة %d: %s", i, time.Since(t0).Round(time.Millisecond))
	}
	if running, _, _ := s.pool.info(); running < 1 || running > 2 {
		t.Errorf("عدد العمليات = %d", running)
	}
}

// الرسم يجب أن يطابق الرسم المرجعي (cairosvg/python-chess) بصرياً.
func TestRenderMatchesReference(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		golden, fen string
		flipped     bool
		from, to    int
	}{
		{"testdata/golden_check_black.png", "rnb1kbnr/pppp1ppp/8/4p3/6Pq/5P2/PPPPP2P/RNBQKBNR w KQkq - 1 3", true, 59, 31}, // d8→h4
		{"testdata/golden_white.png", "r1bqkb1r/pppp1ppp/2n2n2/4p3/2B1P3/5N2/PPPP1PPP/RNBQK2R w KQkq - 4 4", false, 6, 21}, // g1→f3
	}
	for _, c := range cases {
		p := mustFEN(t, c.fen)
		raw, _ := base64.StdEncoding.DecodeString(r.Render(p, c.flipped, c.from, c.to))
		if dir := os.Getenv("CHESS_DUMP_DIR"); dir != "" {
			_ = os.WriteFile(dir+"/"+strings.ReplaceAll(c.golden, "/", "_"), raw, 0o644)
		}
		got, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(c.golden)
		if err != nil {
			t.Fatal(err)
		}
		want, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if d := meanDiff(got, want); d > 5.0 {
			t.Errorf("%s: متوسط الفرق %.2f > 5", c.golden, d)
		} else {
			t.Logf("%s: diff=%.3f", c.golden, d)
		}
	}
}

func meanDiff(a, b image.Image) float64 {
	bd := a.Bounds()
	if bd != b.Bounds() {
		return 255
	}
	var sum float64
	for y := bd.Min.Y; y < bd.Max.Y; y++ {
		for x := bd.Min.X; x < bd.Max.X; x++ {
			r1, g1, b1, _ := a.At(x, y).RGBA()
			r2, g2, b2, _ := b.At(x, y).RGBA()
			sum += absDiff(r1>>8, r2>>8) + absDiff(g1>>8, g2>>8) + absDiff(b1>>8, b2>>8)
		}
	}
	return sum / float64(bd.Dx()*bd.Dy()*3)
}

// absDiff: |a-b| للقيم غير المُوقَّعة (بدون تحويل int وبدون اسم abs المضلِّل).
func absDiff(a, b uint32) float64 {
	if a > b {
		return float64(a - b)
	}
	return float64(b - a)
}

func BenchmarkRenderUncached(b *testing.B) {
	r, _ := newRenderer()
	p, _ := ParseFEN("r1bqkb1r/pppp1ppp/2n2n2/4p3/2B1P3/5N2/PPPP1PPP/RNBQK2R w KQkq - 4 4")
	for i := 0; i < b.N; i++ {
		r.cache = newLRU(4)
		r.Render(p, false, 6, 21)
	}
}
