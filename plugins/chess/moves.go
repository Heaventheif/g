package chess

// moves.go — النقلات: توليد القانونية، التنفيذ، تحليل UCI، وPerft.

import (
	"fmt"
	"strings"
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

func (p *Position) hasLegalEP() bool {
	for _, m := range p.LegalMoves() {
		if int8(m.To) == p.ep && absPiece(p.sq[m.From]) == pawn {
			return true
		}
	}
	return false
}

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
