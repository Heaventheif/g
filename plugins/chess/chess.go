// Package chess — لعبة الشطرنج: POST /process_move
//
// يستقبل FEN + نقلة (UCI) ويعيد FEN جديداً + صورة PNG (base64) — نفس عقد
// chess.py السابق حرفياً. تسلسل محرك النقلات:
//
//	① Lichess Cloud Eval (مجاني، مع كاش ومهلة قصيرة)
//	② Stockfish المحلي (عمليات دائمة)
//	③ نقلة عشوائية قانونية (احتياطي)
//
// كل شيء هنا Go نقي: قواعد اللعبة (position.go)، المحرك (engine.go)،
// ورسم الرقعة (render.go) — لا Python ولا cairo.
package chess

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"sunkenbot/internal/httpx"
	"sunkenbot/internal/plugins"
)

const Description = "محرك شطرنج رسومي (Lichess + Stockfish) — صور PNG لشات فيسبوك"

// difficulty (1-20) → عمق Stockfish (نفس جدول النسخة السابقة).
var depthTable = map[int]int{1: 2, 2: 3, 3: 4, 4: 5, 5: 6, 6: 7, 7: 8, 8: 9, 9: 12, 10: 15, 15: 18, 20: 22}

func depthFor(difficulty int) int {
	if d, ok := depthTable[difficulty]; ok {
		return d
	}
	return 10
}

type Service struct {
	r              *renderer
	pool           *enginePool
	lichess        *lichessClient
	maxLichessMove int
}

// New يبني الخدمة ويُسخّن Stockfish في الخلفية (تحميل الشبكة قبل أول طلب).
func New() *Service {
	s := newService()
	pool := s.pool
	go func() {
		t0 := time.Now()
		if err := pool.warm(); err != nil {
			log.Printf("⚠️  [chess] stockfish غير متاح (%v) — Lichess والاحتياطي العشوائي يعملان", err)
			return
		}
		log.Printf("✅ [chess] stockfish جاهز (تسخين %s)", time.Since(t0).Round(time.Millisecond))
	}()
	return s
}

func newService() *Service {
	r, err := newRenderer()
	if err != nil {
		panic(fmt.Sprintf("chess: تعذّر تحميل أصول الرسم المضمّنة: %v", err)) // خطأ بناء، لا خطأ وقت تشغيل
	}
	get := os.Getenv
	path := get("STOCKFISH_PATH")
	if path == "" {
		path = "stockfish"
	}
	size := envInt(get, "STOCKFISH_POOL", min(2, max(1, runtime.NumCPU())))
	s := &Service{
		r: r,
		pool: newEnginePool(path, size, map[string]string{
			"Hash":    strconv.Itoa(envInt(get, "STOCKFISH_HASH", 32)),
			"Threads": strconv.Itoa(envInt(get, "STOCKFISH_THREADS", 1)),
		}),
		lichess: newLichess(get("LICHESS_TOKEN"),
			time.Duration(envInt(get, "LICHESS_TIMEOUT_MS", 3000))*time.Millisecond),
		maxLichessMove: envInt(get, "LICHESS_MAX_FULLMOVE", 0),
	}
	return s
}

func (s *Service) Name() string { return "chess" }

// Close يُنهي عمليات Stockfish (يُستدعى من main عند الإيقاف السليم).
func (s *Service) Close() error {
	s.pool.close()
	return nil
}

func (s *Service) Routes() []plugins.Route {
	return []plugins.Route{
		{Method: "POST", Pattern: "/process_move", Handler: httpx.WrapJSON(s.handleProcessMove)},
	}
}

type moveRequest struct {
	FEN         string `json:"fen"`
	Move        string `json:"move"`
	BotMode     bool   `json:"bot_mode"`
	Difficulty  *int   `json:"difficulty"`
	Perspective string `json:"perspective"`
}

type moveResponse struct {
	NewFEN      string  `json:"new_fen"`
	ImageBase64 string  `json:"image_base64"`
	GameOver    bool    `json:"game_over"`
	Winner      *string `json:"winner"`
}

func winnerLabel(result string) *string {
	var w string
	switch result {
	case "1-0":
		w = "أبيض"
	case "0-1":
		w = "أسود"
	default:
		return nil
	}
	return &w
}

func (s *Service) handleProcessMove(ctx context.Context, req moveRequest) (moveResponse, error) {
	t0 := time.Now()
	if strings.TrimSpace(req.FEN) == "" {
		return moveResponse{}, &httpx.HTTPError{Code: http.StatusUnprocessableEntity, Body: map[string]string{"detail": "fen مطلوب"}}
	}
	difficulty := 10
	if req.Difficulty != nil {
		difficulty = *req.Difficulty
	}
	flipped := req.Perspective == "black"

	pos, err := ParseFEN(req.FEN)
	if err != nil {
		return moveResponse{}, &httpx.HTTPError{Code: http.StatusBadRequest, Body: map[string]string{"detail": "FEN غير صالح"}}
	}
	lastFrom, lastTo := -1, -1

	// ── نقلة اللاعب ──────────────────────────────────────────────────────────
	if raw := strings.TrimSpace(req.Move); raw != "" {
		m, perr := pos.ParseUCI(raw)
		if perr != nil {
			return moveResponse{}, &httpx.HTTPError{Code: http.StatusUnprocessableEntity, Body: map[string]string{"detail": fmt.Sprintf(
				"❌ صيغة خاطئة: %s\nاستخدم صيغة UCI مثل: e2e4", strings.ToUpper(raw))}}
		}
		if !pos.IsLegal(m) {
			return moveResponse{}, &httpx.HTTPError{Code: http.StatusUnprocessableEntity, Body: map[string]string{"detail": fmt.Sprintf(
				"❌ نقلة غير قانونية: %s\nمثال صحيح: e2e4 أو e7e8q", strings.ToUpper(raw))}}
		}
		next := pos.Apply(m)
		pos = &next
		lastFrom, lastTo = int(m.From), int(m.To)
	}

	// ── نهاية اللعبة بعد نقلة اللاعب ─────────────────────────────────────────
	if outcome := pos.Outcome(); outcome != "*" {
		log.Printf("[chess] game_over result=%s (%s)", outcome, time.Since(t0).Round(time.Millisecond))
		return moveResponse{
			NewFEN:      pos.FEN(),
			ImageBase64: s.r.Render(pos, flipped, lastFrom, lastTo),
			GameOver:    true,
			Winner:      winnerLabel(outcome),
		}, nil
	}

	// ── نقلة البوت (حتى لو move فارغة: قد يكون البوت هو الأبيض) ──────────────
	if req.BotMode {
		if uci := s.engineMove(ctx, pos, difficulty); uci != "" {
			if m, err := pos.ParseUCI(uci); err == nil && pos.IsLegal(m) {
				next := pos.Apply(m)
				pos = &next
				lastFrom, lastTo = int(m.From), int(m.To)
			}
		}
	}

	outcome := pos.Outcome()
	log.Printf("[chess] done game_over=%v (%s)", outcome != "*", time.Since(t0).Round(time.Millisecond))
	return moveResponse{
		NewFEN:      pos.FEN(),
		ImageBase64: s.r.Render(pos, flipped, lastFrom, lastTo),
		GameOver:    outcome != "*",
		Winner:      winnerLabel(outcome),
	}, nil
}

// engineMove يجرّب المصادر بالترتيب ويعيد أفضل نقلة UCI متاحة.
func (s *Service) engineMove(ctx context.Context, pos *Position, difficulty int) string {
	fen := pos.FEN()

	if s.maxLichessMove == 0 || pos.Fullmove() <= s.maxLichessMove {
		if mv := s.lichess.move(ctx, fen); mv != "" {
			log.Printf("[chess:lichess] %s", mv)
			return mv
		}
	}

	depth := depthFor(difficulty)
	movetime := time.Duration(min(float64(difficulty)*0.15, 4.0) * float64(time.Second))
	movetime = max(movetime, 50*time.Millisecond)
	if mv, err := s.pool.play(ctx, fen, depth, movetime); err == nil && mv != "" {
		log.Printf("[chess:stockfish] depth=%d → %s", depth, mv)
		return mv
	} else if err != nil {
		log.Printf("[chess:stockfish] %v", err)
	}

	// ③ احتياطي عشوائي — دائماً يعمل
	if legal := pos.LegalMoves(); len(legal) > 0 {
		mv := legal[rand.IntN(len(legal))].UCI()
		log.Printf("[chess:fallback] نقلة عشوائية: %s", mv)
		return mv
	}
	return ""
}
