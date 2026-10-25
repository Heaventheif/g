package chess

// outcome.go — نهاية اللعبة: كش مات، تعادل، مادة غير كافية.

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
