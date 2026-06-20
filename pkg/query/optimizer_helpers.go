package query

// 本文件聚合优化器共享的辅助函数：
//   - 列引用收集：collectColumnRefs / collectColumnRefsInto
//   - 合取范式拆分与合并：splitConjuncts / mergeConjuncts
//
// 拆分动机：原 optimizer.go 同时承载 3 条优化规则 + 调度逻辑 + 大量辅助
// 函数，单文件行数逼近 500 行 CI 硬上限，且多规则互相干扰阅读。
// 拆分后每条规则独立成文件、共享逻辑收敛于 helpers，便于单点修改与
// 未来扩展新规则（如 JoinReorder / SubqueryDecorrelate）时只新增文件。

// collectColumnRefs 收集表达式树中的列引用名（去重，返回顺序无保证）。
// 预分配常见列数 (8)，减少 map 扩容。
func collectColumnRefs(expr Expression) []string {
	seen := make(map[string]bool, 8) // 预分配常见列数，减少扩容
	collectColumnRefsInto(expr, seen)
	result := make([]string, 0, len(seen))
	for k := range seen {
		result = append(result, k)
	}
	return result
}

// collectColumnRefsInto 递归收集表达式树中的列引用名至 seen。
// 这是 analyzer.collectRequiredColumns 与 optimizer 列裁剪共用的唯一实现，
// 消除原先 analyzer.collectExprColumns 的重复逻辑。
// expr 可为 nil（例如 SELECT 无 WHERE 子句时 sel.Where 为 nil），此时直接返回。
func collectColumnRefsInto(expr Expression, seen map[string]bool) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ColumnExpr:
		seen[e.Name] = true
	case *ResolvedColumnExpr:
		seen[e.Name] = true
	case *BinaryExpr:
		collectColumnRefsInto(e.Left, seen)
		collectColumnRefsInto(e.Right, seen)
	case *UnaryExpr:
		collectColumnRefsInto(e.Expr, seen)
	case *FuncExpr:
		for _, arg := range e.Args {
			collectColumnRefsInto(arg, seen)
		}
	}
}

// splitConjuncts 将顶层 AND 表达式拆分为子句列表。
// 非 AND 表达式或单条表达式原样返回长度为 1 的切片。
func splitConjuncts(expr Expression) []Expression {
	bin, ok := expr.(*BinaryExpr)
	if !ok || bin.Op != OpAnd {
		return []Expression{expr}
	}
	return append(splitConjuncts(bin.Left), splitConjuncts(bin.Right)...)
}

// mergeConjuncts 用 AND 将子句列表合并为单个表达式。
// 空列表返回 nil（调用方据此判断无可合并项）。
func mergeConjuncts(conjuncts []Expression) Expression {
	if len(conjuncts) == 0 {
		return nil
	}
	result := conjuncts[0]
	for _, c := range conjuncts[1:] {
		result = &BinaryExpr{Op: OpAnd, Left: result, Right: c}
	}
	return result
}
