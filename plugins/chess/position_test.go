package chess

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

func mustFEN(t *testing.T, fen string) *Position {
	t.Helper()
	p, err := ParseFEN(fen)
	if err != nil {
		t.Fatalf("ParseFEN(%q): %v", fen, err)
	}
	return p
}

// أعداد perft المعروفة (chessprogramming.org/Perft_Results).
func TestPerft(t *testing.T) {
	cases := []struct {
		name string
		fen  string
		want []uint64
	}{
		{"start", "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", []uint64{20, 400, 8902, 197281}},
		{"kiwipete", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", []uint64{48, 2039, 97862}},
		{"pos3", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", []uint64{14, 191, 2812, 43238}},
		{"pos4", "r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1", []uint64{6, 264, 9467}},
		{"pos5", "rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8", []uint64{44, 1486, 62379}},
		{"pos6", "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10", []uint64{46, 2079, 89890}},
	}
	for _, c := range cases {
		p := mustFEN(t, c.fen)
		for i, want := range c.want {
			if got := p.Perft(i + 1); got != want {
				t.Errorf("%s perft(%d) = %d, want %d", c.name, i+1, got, want)
			}
		}
	}
}

// مقارنة كاملة مع python-chess على آلاف المواضع: النقلات القانونية، FEN، النتيجة.
func TestAgainstPythonChess(t *testing.T) {
	raw, err := os.ReadFile("testdata/positions.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		FEN    string   `json:"fen"`
		Moves  []string `json:"moves"`
		Over   bool     `json:"over"`
		Result string   `json:"result"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		p := mustFEN(t, r.FEN)
		var got []string
		for _, m := range p.LegalMoves() {
			got = append(got, m.UCI())
		}
		sort.Strings(got)
		if strings.Join(got, " ") != strings.Join(r.Moves, " ") {
			t.Fatalf("نقلات مختلفة لـ %s\n go: %v\n py: %v", r.FEN, got, r.Moves)
		}
		if p.FEN() != r.FEN {
			t.Fatalf("FEN مختلف:\n go: %s\n py: %s", p.FEN(), r.FEN)
		}
		if p.GameOver() != r.Over {
			t.Fatalf("game_over مختلف لـ %s: go=%v py=%v", r.FEN, p.GameOver(), r.Over)
		}
		if r.Over && p.Outcome() != r.Result {
			t.Fatalf("النتيجة مختلفة لـ %s: go=%s py=%s", r.FEN, p.Outcome(), r.Result)
		}
	}
	t.Logf("%d موضعاً طابقت python-chess", len(rows))
}

func TestApplyMatchesFENRoundTrip(t *testing.T) {
	p := mustFEN(t, "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	for _, u := range []string{"e2e4", "e7e5", "g1f3", "b8c6", "f1b5"} {
		m, err := p.ParseUCI(u)
		if err != nil || !p.IsLegal(m) {
			t.Fatalf("%s يجب أن تكون قانونية (%v)", u, err)
		}
		n := p.Apply(m)
		p = &n
	}
	want := "r1bqkbnr/pppp1ppp/2n5/1B2p3/4P3/5N2/PPPP1PPP/RNBQK2R b KQkq - 3 3"
	if got := p.FEN(); got != want {
		t.Errorf("FEN = %s\nwant  %s", got, want)
	}
}

func TestParseUCI(t *testing.T) {
	p := mustFEN(t, "8/P6k/8/8/8/8/8/K7 w - - 0 1")
	m, err := p.ParseUCI("a7a8")
	if err != nil || m.Promo != queen {
		t.Errorf("الترقية التلقائية للوزير فشلت: %+v %v", m, err)
	}
	for _, bad := range []string{"", "e2", "e2e9", "z1a1", "e2e4x", "e7e8k"} {
		if _, err := p.ParseUCI(bad); err == nil {
			t.Errorf("%q يجب أن يُرفض", bad)
		}
	}
}

func TestBadFEN(t *testing.T) {
	for _, bad := range []string{"", "x", "8/8/8/8/8/8/8 w - - 0 1", "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR x KQkq - 0 1", "8/8/8/8/8/8/8/8 w - - 0 1"} {
		if _, err := ParseFEN(bad); err == nil {
			t.Errorf("FEN %q يجب أن يُرفض", bad)
		}
	}
}

func BenchmarkLegalMoves(b *testing.B) {
	p, _ := ParseFEN("r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1")
	for i := 0; i < b.N; i++ {
		p.LegalMoves()
	}
}
