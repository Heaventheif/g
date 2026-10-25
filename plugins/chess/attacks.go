package chess

// attacks.go — الهجوم والكش وأدوات المربعات.

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
