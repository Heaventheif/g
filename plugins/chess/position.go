package chess

// position.go — قواعد الشطرنج: تمثيل الرقعة، FEN، توليد النقلات القانونية،
// ونهاية اللعبة. مكتوب بلغة Go النقية (stdlib فقط) ومتحقَّق منه بـ perft
// وبمقارنة مع python-chess (راجع position_test.go).
//
// الترقيم: sq = rank*8 + file  (a1 = 0، h1 = 7، a8 = 56، h8 = 63) — نفس python-chess.

import (
	"fmt"
	"strconv"
	"strings"
)

// القطع: موجب = أبيض، سالب = أسود، 0 = فارغ.
const (
	pawn   int8 = 1
	knight int8 = 2
	bishop int8 = 3
	rook   int8 = 4
	queen  int8 = 5
	king   int8 = 6
)

// حقوق التبييت.
const (
	castleWK uint8 = 1
	castleWQ uint8 = 2
	castleBK uint8 = 4
	castleBQ uint8 = 8
)

const (
	white = true
	black = false
)

// Move: نقلة واحدة. promo = 0 أو نوع القطعة المرقّى إليها.
type Move struct {
	From, To uint8
	Promo    int8
}

// UCI يعيد النقلة بصيغة UCI مثل e2e4 أو e7e8q.
func (m Move) UCI() string {
	s := sqName(m.From) + sqName(m.To)
	switch m.Promo {
	case queen:
		s += "q"
	case rook:
		s += "r"
	case bishop:
		s += "b"
	case knight:
		s += "n"
	}
	return s
}

func sqName(s uint8) string {
	return string([]byte{'a' + s%8, '1' + s/8})
}

// Position: موضع كامل. قيمة صغيرة (≈ 80 بايت) تُنسخ بلا تخصيص ذاكرة.
type Position struct {
	sq       [64]int8
	whiteTo  bool
	castling uint8
	ep       int8 // مربع الأخذ بالمرور، أو -1
	halfmove int
	fullmove int
}

var (
	knightDeltas = [8][2]int{{1, 2}, {2, 1}, {2, -1}, {1, -2}, {-1, -2}, {-2, -1}, {-2, 1}, {-1, 2}}
	kingDeltas   = [8][2]int{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}
	bishopDirs   = [4][2]int{{1, 1}, {-1, 1}, {-1, -1}, {1, -1}}
	rookDirs     = [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
)

func onBoard(f, r int) bool { return f >= 0 && f < 8 && r >= 0 && r < 8 }

func isWhitePiece(p int8) bool { return p > 0 }

func absPiece(p int8) int8 {
	if p < 0 {
		return -p
	}
	return p
}

// ParseFEN يحلّل FEN (يتسامح مع غياب الحقول الأخيرة كما يفعل python-chess).
func ParseFEN(fen string) (*Position, error) {
	parts := strings.Fields(fen)
	if len(parts) == 0 {
		return nil, fmt.Errorf("fen فارغ")
	}
	p := &Position{whiteTo: white, ep: -1, fullmove: 1}

	ranks := strings.Split(parts[0], "/")
	if len(ranks) != 8 {
		return nil, fmt.Errorf("عدد الصفوف %d ≠ 8", len(ranks))
	}
	for i, row := range ranks {
		rank := 7 - i
		file := 0
		for _, c := range row {
			switch {
			case c >= '1' && c <= '8':
				file += int(c - '0')
			default:
				t, ok := pieceFromChar(byte(c))
				if !ok || file > 7 {
					return nil, fmt.Errorf("رمز غير صالح %q", c)
				}
				p.sq[rank*8+file] = t
				file++
			}
			if file > 8 {
				return nil, fmt.Errorf("الصف %d أطول من 8", rank+1)
			}
		}
		if file != 8 {
			return nil, fmt.Errorf("الصف %d لا يساوي 8 مربعات", rank+1)
		}
	}

	if len(parts) > 1 {
		switch parts[1] {
		case "w":
			p.whiteTo = white
		case "b":
			p.whiteTo = black
		default:
			return nil, fmt.Errorf("الدور %q غير صالح", parts[1])
		}
	}
	if len(parts) > 2 && parts[2] != "-" {
		for _, c := range parts[2] {
			switch c {
			case 'K':
				p.castling |= castleWK
			case 'Q':
				p.castling |= castleWQ
			case 'k':
				p.castling |= castleBK
			case 'q':
				p.castling |= castleBQ
			default:
				return nil, fmt.Errorf("حقوق التبييت %q غير صالحة", parts[2])
			}
		}
	}
	if len(parts) > 3 && parts[3] != "-" {
		if len(parts[3]) != 2 || parts[3][0] < 'a' || parts[3][0] > 'h' || parts[3][1] < '1' || parts[3][1] > '8' {
			return nil, fmt.Errorf("مربع المرور %q غير صالح", parts[3])
		}
		p.ep = int8((parts[3][1]-'1')*8 + (parts[3][0] - 'a'))
	}
	if len(parts) > 4 {
		n, err := strconv.Atoi(parts[4])
		if err != nil || n < 0 {
			return nil, fmt.Errorf("halfmove غير صالح")
		}
		p.halfmove = n
	}
	if len(parts) > 5 {
		n, err := strconv.Atoi(parts[5])
		if err != nil || n < 1 {
			return nil, fmt.Errorf("fullmove غير صالح")
		}
		p.fullmove = n
	}

	// ملوك: واحد لكل لون بالضبط، وإلا فالمنطق غير معرَّف.
	var wk, bk int
	for _, t := range p.sq {
		if t == king {
			wk++
		} else if t == -king {
			bk++
		}
	}
	if wk != 1 || bk != 1 {
		return nil, fmt.Errorf("يجب وجود ملك واحد لكل لون")
	}
	// بيادق في الصفين الأول/الأخير غير مسموحة
	for f := 0; f < 8; f++ {
		if absPiece(p.sq[f]) == pawn || absPiece(p.sq[56+f]) == pawn {
			return nil, fmt.Errorf("بيدق في الصف الأول أو الأخير")
		}
	}
	p.cleanCastling()
	return p, nil
}

func pieceFromChar(c byte) (int8, bool) {
	var t int8
	switch c | 0x20 {
	case 'p':
		t = pawn
	case 'n':
		t = knight
	case 'b':
		t = bishop
	case 'r':
		t = rook
	case 'q':
		t = queen
	case 'k':
		t = king
	default:
		return 0, false
	}
	if c >= 'a' { // حرف صغير = أسود
		t = -t
	}
	return t, true
}

func pieceChar(t int8) byte {
	var c byte
	switch absPiece(t) {
	case pawn:
		c = 'p'
	case knight:
		c = 'n'
	case bishop:
		c = 'b'
	case rook:
		c = 'r'
	case queen:
		c = 'q'
	case king:
		c = 'k'
	}
	if t > 0 {
		c -= 0x20
	}
	return c
}

// cleanCastling يزيل حقوق التبييت التي لا يوجد لها ملك/رخ في مكانه.
func (p *Position) cleanCastling() {
	if p.sq[4] != king {
		p.castling &^= castleWK | castleWQ
	}
	if p.sq[60] != -king {
		p.castling &^= castleBK | castleBQ
	}
	if p.sq[7] != rook {
		p.castling &^= castleWK
	}
	if p.sq[0] != rook {
		p.castling &^= castleWQ
	}
	if p.sq[63] != -rook {
		p.castling &^= castleBK
	}
	if p.sq[56] != -rook {
		p.castling &^= castleBQ
	}
}

// BoardFEN يعيد حقل القطع فقط.
func (p *Position) BoardFEN() string {
	var sb strings.Builder
	for rank := 7; rank >= 0; rank-- {
		empty := 0
		for file := 0; file < 8; file++ {
			t := p.sq[rank*8+file]
			if t == 0 {
				empty++
				continue
			}
			if empty > 0 {
				sb.WriteByte(byte('0' + empty))
				empty = 0
			}
			sb.WriteByte(pieceChar(t))
		}
		if empty > 0 {
			sb.WriteByte(byte('0' + empty))
		}
		if rank > 0 {
			sb.WriteByte('/')
		}
	}
	return sb.String()
}

// FEN يعيد FEN كاملاً. مربع الأخذ بالمرور يُكتب فقط إن وُجد أخذ قانوني فعلاً
// (نفس سلوك python-chess الافتراضي).
func (p *Position) FEN() string {
	turn := "b"
	if p.whiteTo {
		turn = "w"
	}
	cast := ""
	if p.castling&castleWK != 0 {
		cast += "K"
	}
	if p.castling&castleWQ != 0 {
		cast += "Q"
	}
	if p.castling&castleBK != 0 {
		cast += "k"
	}
	if p.castling&castleBQ != 0 {
		cast += "q"
	}
	if cast == "" {
		cast = "-"
	}
	ep := "-"
	if p.ep >= 0 && p.hasLegalEP() {
		ep = sqName(uint8(p.ep))
	}
	return fmt.Sprintf("%s %s %s %s %d %d", p.BoardFEN(), turn, cast, ep, p.halfmove, p.fullmove)
}

func (p *Position) hasLegalEP() bool {
	for _, m := range p.LegalMoves() {
		if int8(m.To) == p.ep && absPiece(p.sq[m.From]) == pawn {
			return true
		}
	}
	return false
}

// Turn: true = دور الأبيض.
func (p *Position) WhiteToMove() bool { return p.whiteTo }

// Fullmove يعيد رقم النقلة الكاملة.
func (p *Position) Fullmove() int { return p.fullmove }

func (p *Position) kingSquare(whiteSide bool) int {
	k := king
	if !whiteSide {
		k = -king
	}
	for i, t := range p.sq {
		if t == k {
			return i
		}
	}
	return -1
}

// attacked: هل المربع s مهاجَم من قطع اللون byWhite؟
func (p *Position) attacked(s int, byWhite bool) bool {
	f, r := s%8, s/8
	sign := int8(1)
	if !byWhite {
		sign = -1
	}
	// بيادق: الأبيض يهاجم للأعلى، فالبيدق المهاجِم يقع تحت المربع
	pr := r - 1
	if !byWhite {
		pr = r + 1
	}
	for _, df := range [2]int{-1, 1} {
		if onBoard(f+df, pr) && p.sq[pr*8+f+df] == sign*pawn {
			return true
		}
	}
	for _, d := range knightDeltas {
		nf, nr := f+d[0], r+d[1]
		if onBoard(nf, nr) && p.sq[nr*8+nf] == sign*knight {
			return true
		}
	}
	for _, d := range kingDeltas {
		nf, nr := f+d[0], r+d[1]
		if onBoard(nf, nr) && p.sq[nr*8+nf] == sign*king {
			return true
		}
	}
	for _, d := range bishopDirs {
		for nf, nr := f+d[0], r+d[1]; onBoard(nf, nr); nf, nr = nf+d[0], nr+d[1] {
			t := p.sq[nr*8+nf]
			if t == 0 {
				continue
			}
			if t == sign*bishop || t == sign*queen {
				return true
			}
			break
		}
	}
	for _, d := range rookDirs {
		for nf, nr := f+d[0], r+d[1]; onBoard(nf, nr); nf, nr = nf+d[0], nr+d[1] {
			t := p.sq[nr*8+nf]
			if t == 0 {
				continue
			}
			if t == sign*rook || t == sign*queen {
				return true
			}
			break
		}
	}
	return false
}

// InCheck: هل صاحب الدور في كش؟
func (p *Position) InCheck() bool {
	ks := p.kingSquare(p.whiteTo)
	return ks >= 0 && p.attacked(ks, !p.whiteTo)
}

// KingSquare يعيد مربع ملك صاحب الدور (للرسم).
func (p *Position) KingSquare() int { return p.kingSquare(p.whiteTo) }

func (p *Position) pseudoMoves(out []Move) []Move {
	for s := 0; s < 64; s++ {
		t := p.sq[s]
		if t == 0 || (t > 0) != p.whiteTo {
			continue
		}
		f, r := s%8, s/8
		switch absPiece(t) {
		case pawn:
			dir, start, last := 1, 1, 7
			if !p.whiteTo {
				dir, start, last = -1, 6, 0
			}
			nr := r + dir
			if onBoard(f, nr) && p.sq[nr*8+f] == 0 {
				out = appendPawn(out, s, nr*8+f, nr == last)
				if r == start && p.sq[(r+2*dir)*8+f] == 0 {
					out = append(out, Move{uint8(s), uint8((r+2*dir)*8 + f), 0})
				}
			}
			for _, df := range [2]int{-1, 1} {
				nf := f + df
				if !onBoard(nf, nr) {
					continue
				}
				to := nr*8 + nf
				target := p.sq[to]
				if target != 0 && (target > 0) != p.whiteTo {
					out = appendPawn(out, s, to, nr == last)
				} else if target == 0 && int8(to) == p.ep && ((p.whiteTo && nr == 5) || (!p.whiteTo && nr == 2)) {
					out = append(out, Move{uint8(s), uint8(to), 0})
				}
			}
		case knight:
			for _, d := range knightDeltas {
				out = p.stepMove(out, s, f+d[0], r+d[1])
			}
		case king:
			for _, d := range kingDeltas {
				out = p.stepMove(out, s, f+d[0], r+d[1])
			}
			out = p.castleMoves(out, s)
		case bishop:
			out = p.slideMoves(out, s, bishopDirs[:])
		case rook:
			out = p.slideMoves(out, s, rookDirs[:])
		case queen:
			out = p.slideMoves(out, s, bishopDirs[:])
			out = p.slideMoves(out, s, rookDirs[:])
		}
	}
	return out
}

func appendPawn(out []Move, from, to int, promote bool) []Move {
	if !promote {
		return append(out, Move{uint8(from), uint8(to), 0})
	}
	for _, pr := range [4]int8{queen, rook, bishop, knight} {
		out = append(out, Move{uint8(from), uint8(to), pr})
	}
	return out
}

func (p *Position) stepMove(out []Move, from, nf, nr int) []Move {
	if !onBoard(nf, nr) {
		return out
	}
	t := p.sq[nr*8+nf]
	if t != 0 && (t > 0) == p.whiteTo {
		return out
	}
	return append(out, Move{uint8(from), uint8(nr*8 + nf), 0})
}

func (p *Position) slideMoves(out []Move, from int, dirs [][2]int) []Move {
	f, r := from%8, from/8
	for _, d := range dirs {
		for nf, nr := f+d[0], r+d[1]; onBoard(nf, nr); nf, nr = nf+d[0], nr+d[1] {
			t := p.sq[nr*8+nf]
			if t == 0 {
				out = append(out, Move{uint8(from), uint8(nr*8 + nf), 0})
				continue
			}
			if (t > 0) != p.whiteTo {
				out = append(out, Move{uint8(from), uint8(nr*8 + nf), 0})
			}
			break
		}
	}
	return out
}

func (p *Position) castleMoves(out []Move, from int) []Move {
	if p.whiteTo {
		if from != 4 || p.attacked(4, false) {
			return out
		}
		if p.castling&castleWK != 0 && p.sq[5] == 0 && p.sq[6] == 0 && !p.attacked(5, false) && !p.attacked(6, false) {
			out = append(out, Move{4, 6, 0})
		}
		if p.castling&castleWQ != 0 && p.sq[1] == 0 && p.sq[2] == 0 && p.sq[3] == 0 && !p.attacked(3, false) && !p.attacked(2, false) {
			out = append(out, Move{4, 2, 0})
		}
		return out
	}
	if from != 60 || p.attacked(60, true) {
		return out
	}
	if p.castling&castleBK != 0 && p.sq[61] == 0 && p.sq[62] == 0 && !p.attacked(61, true) && !p.attacked(62, true) {
		out = append(out, Move{60, 62, 0})
	}
	if p.castling&castleBQ != 0 && p.sq[57] == 0 && p.sq[58] == 0 && p.sq[59] == 0 && !p.attacked(59, true) && !p.attacked(58, true) {
		out = append(out, Move{60, 58, 0})
	}
	return out
}

// Apply يُنفّذ نقلة (يُفترض أنها قانونية) ويعيد الموضع الجديد.
func (p Position) Apply(m Move) Position {
	n := p
	piece := n.sq[m.From]
	captured := n.sq[m.To]
	n.sq[m.From] = 0
	n.ep = -1

	switch absPiece(piece) {
	case pawn:
		if int8(m.To) == p.ep && captured == 0 && m.From%8 != m.To%8 {
			// أخذ بالمرور: يُزال البيدق المأخوذ خلف مربع الهدف
			if p.whiteTo {
				n.sq[m.To-8] = 0
			} else {
				n.sq[m.To+8] = 0
			}
		}
		if int(m.To)-int(m.From) == 16 || int(m.From)-int(m.To) == 16 {
			n.ep = int8((int(m.From) + int(m.To)) / 2)
		}
	case king:
		// تبييت: حرّك الرخ أيضاً
		if m.From == 4 && m.To == 6 || m.From == 60 && m.To == 62 {
			n.sq[m.To-1], n.sq[m.To+1] = n.sq[m.To+1], 0
		} else if m.From == 4 && m.To == 2 || m.From == 60 && m.To == 58 {
			n.sq[m.To+1], n.sq[m.To-2] = n.sq[m.To-2], 0
		}
	}

	if m.Promo != 0 {
		if p.whiteTo {
			piece = m.Promo
		} else {
			piece = -m.Promo
		}
	}
	n.sq[m.To] = piece

	if absPiece(piece) == pawn || captured != 0 {
		n.halfmove = 0
	} else {
		n.halfmove++
	}
	if !p.whiteTo {
		n.fullmove++
	}
	n.whiteTo = !p.whiteTo
	n.cleanCastling()
	return n
}

// LegalMoves يولّد كل النقلات القانونية.
func (p *Position) LegalMoves() []Move {
	var buf [128]Move
	pseudo := p.pseudoMoves(buf[:0])
	legal := make([]Move, 0, len(pseudo))
	for _, m := range pseudo {
		n := p.Apply(m)
		ks := n.kingSquare(p.whiteTo)
		if ks >= 0 && !n.attacked(ks, !p.whiteTo) {
			legal = append(legal, m)
		}
	}
	return legal
}

// ParseUCI يحلّل نقلة UCI ويضيف ترقية وزير تلقائياً لبيدق يصل الصف الأخير بلا حرف.
// يعيد خطأ إن كانت الصيغة خاطئة (لا القانونية — هذه يفحصها IsLegal).
func (p *Position) ParseUCI(s string) (Move, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 4 && len(s) != 5 {
		return Move{}, fmt.Errorf("صيغة UCI خاطئة")
	}
	if s[0] < 'a' || s[0] > 'h' || s[2] < 'a' || s[2] > 'h' ||
		s[1] < '1' || s[1] > '8' || s[3] < '1' || s[3] > '8' {
		return Move{}, fmt.Errorf("صيغة UCI خاطئة")
	}
	m := Move{From: (s[1]-'1')*8 + (s[0] - 'a'), To: (s[3]-'1')*8 + (s[2] - 'a')}
	if len(s) == 5 {
		switch s[4] {
		case 'q':
			m.Promo = queen
		case 'r':
			m.Promo = rook
		case 'b':
			m.Promo = bishop
		case 'n':
			m.Promo = knight
		default:
			return Move{}, fmt.Errorf("حرف الترقية خاطئ")
		}
		return m, nil
	}
	if t := p.sq[m.From]; absPiece(t) == pawn {
		rank := m.To / 8
		if (t > 0 && rank == 7) || (t < 0 && rank == 0) {
			m.Promo = queen
		}
	}
	return m, nil
}

// IsLegal: هل النقلة ضمن القانونية؟
func (p *Position) IsLegal(m Move) bool {
	for _, l := range p.LegalMoves() {
		if l == m {
			return true
		}
	}
	return false
}

// ── نهاية اللعبة (مطابقة لـ python-chess: is_game_over بلا claim_draw) ──────

// Outcome: النتيجة بصيغة "1-0" / "0-1" / "1/2-1/2" / "*".
func (p *Position) Outcome() string {
	legal := p.LegalMoves()
	if len(legal) == 0 {
		if p.InCheck() {
			if p.whiteTo {
				return "0-1"
			}
			return "1-0"
		}
		return "1/2-1/2"
	}
	if p.insufficientMaterial() {
		return "1/2-1/2"
	}
	if p.halfmove >= 150 {
		return "1/2-1/2"
	}
	return "*"
}

// GameOver: هل انتهت اللعبة؟
func (p *Position) GameOver() bool { return p.Outcome() != "*" }

func (p *Position) insufficientMaterial() bool {
	return p.sideInsufficient(white) && p.sideInsufficient(black)
}

func (p *Position) sideInsufficient(side bool) bool {
	var own, opp int
	var knights, bishops int
	var anyPawn, anyKnightAll bool
	var lightB, darkB bool
	var oppNonKQ int
	for s, t := range p.sq {
		if t == 0 {
			continue
		}
		a := absPiece(t)
		mine := (t > 0) == side
		if a == pawn {
			anyPawn = true
		}
		if a == knight {
			anyKnightAll = true
		}
		if a == bishop {
			if (s%8+s/8)%2 == 1 {
				lightB = true
			} else {
				darkB = true
			}
		}
		if mine {
			own++
			switch a {
			case pawn, rook, queen:
				return false
			case knight:
				knights++
			case bishop:
				bishops++
			}
		} else {
			opp++
			if a != king && a != queen {
				oppNonKQ++
			}
		}
	}
	if knights > 0 {
		return own <= 2 && oppNonKQ == 0
	}
	if bishops > 0 {
		sameColor := !lightB || !darkB
		return sameColor && !anyPawn && !anyKnightAll
	}
	return true
}

// Perft للاختبار: عدد العقد على عمق معيّن.
func (p *Position) Perft(depth int) uint64 {
	if depth == 0 {
		return 1
	}
	moves := p.LegalMoves()
	if depth == 1 {
		return uint64(len(moves))
	}
	var n uint64
	for _, m := range moves {
		c := p.Apply(m)
		n += c.Perft(depth - 1)
	}
	return n
}

// PieceAt يعيد القطعة عند المربع (للرسم).
func (p *Position) PieceAt(s int) int8 { return p.sq[s] }
