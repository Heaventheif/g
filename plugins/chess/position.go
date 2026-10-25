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

// Position: موضع كامل. قيمة صغيرة (≈ 80 بايت) تُنسخ بلا تخصيص ذاكرة.
type Position struct {
	sq       [64]int8
	whiteTo  bool
	castling uint8
	ep       int8 // مربع الأخذ بالمرور، أو -1
	halfmove int
	fullmove int
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

// Turn: true = دور الأبيض.
func (p *Position) WhiteToMove() bool { return p.whiteTo }

// Fullmove يعيد رقم النقلة الكاملة.
func (p *Position) Fullmove() int { return p.fullmove }

// PieceAt يعيد القطعة عند المربع (للرسم).
func (p *Position) PieceAt(s int) int8 { return p.sq[s] }
