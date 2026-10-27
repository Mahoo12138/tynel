package nir

// BlockReturns 报告一个语句块是否在每条路径上都以 return 结束。
//
// 判定是结构性的：一条 return 直接结束；if 只有在 then 与 else 都存在且都
// 结束时才结束。let、print、discard 不改变控制流。return 之后还有语句时，
// 后面的语句不可达，但块本身已经结束——不可达语句仍保留在 NIR 里，由后端
// 决定是否生成。
func BlockReturns(stmts []Stmt) bool {
	for i := range stmts {
		if stmtReturns(&stmts[i]) {
			return true
		}
	}
	return false
}

func stmtReturns(s *Stmt) bool {
	switch s.Op {
	case OpReturn:
		return true
	case OpIf:
		return len(s.Then) > 0 && len(s.Else) > 0 &&
			BlockReturns(s.Then) && BlockReturns(s.Else)
	}
	return false
}

// FirstMissingReturn 返回第一条没有 return 的路径上的位置。nil 表示所有路径
// 都以 return 结束。空块没有位置，调用方应先用 BlockReturns 判断。
func FirstMissingReturn(stmts []Stmt) *Pos {
	for i := range stmts {
		s := &stmts[i]
		switch s.Op {
		case OpReturn:
			return nil
		case OpIf:
			if len(s.Then) == 0 || len(s.Else) == 0 {
				return s.Pos
			}
			if p := FirstMissingReturn(s.Then); p != nil {
				return p
			}
			if p := FirstMissingReturn(s.Else); p != nil {
				return p
			}
			return nil
		}
	}
	if len(stmts) > 0 {
		return stmts[len(stmts)-1].Pos
	}
	return nil
}
