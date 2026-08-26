package raft

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestLockOwnershipDiscipline проверяет по исходному коду пакета дисциплину
// владения блокировкой: блокировка снимается в той же функции, где была взята.
// Нарушением считается функция, которая снимает блокировку, ни разу её не
// взяв, либо берёт, ни разу не сняв.
//
// Чего проверка НЕ делает:
//   - не проверяет симметричность регионов внутри одной функции: пропущенное
//     снятие на одной из нескольких ветвей она не увидит. Частично это
//     закрыто свойством самого кода: отложенный сброс флага активной горутины
//     репликации сам захватывает cm.mu, а нерекурсивный мьютекс превращает
//     выход с удерживаемой блокировкой в немедленный дедлок;
//   - не проверяет корректность самой синхронизации;
//   - не видит захватов через промежуточную переменную (m := &cm.mu;
//     m.Lock()) — такой формы в пакете нет, и её появление само по себе
//     нарушает стиль.
func TestLockOwnershipDiscipline(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("cannot list package files: %v", err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("cannot parse %s: %v", file, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			locks, unlocks := countMutexCalls(fn.Body)
			if (locks == 0) != (unlocks == 0) {
				t.Errorf(
					"%s: %s захватывает блокировку %d раз(а) и снимает %d раз(а): "+
						"блокировка снимается в той же функции, где была взята; функция, "+
						"требующая удержания блокировки, должна нести суффикс Locked в имени",
					file, fn.Name.Name, locks, unlocks,
				)
			}
		}
	}
}

// countMutexCalls считает захваты и снятия мьютекса в теле функции. Обход
// дерева учитывает вызовы внутри defer, внутри литералов функций и внутри
// вложенных блоков.
func countMutexCalls(body *ast.BlockStmt) (locks, unlocks int) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := identifierChain(call.Fun)
		if !ok {
			return true
		}
		switch {
		case strings.HasSuffix(name, ".mu.Lock"), strings.HasSuffix(name, ".mu.RLock"):
			locks++
		case strings.HasSuffix(name, ".mu.Unlock"), strings.HasSuffix(name, ".mu.RUnlock"):
			unlocks++
		}
		return true
	})
	return locks, unlocks
}

// identifierChain восстанавливает текстовый вид вызываемого (cm.mu.Lock).
// Выражения, не сводящиеся к цепочке идентификаторов, отбрасываются.
func identifierChain(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name, true
	case *ast.SelectorExpr:
		prefix, ok := identifierChain(e.X)
		if !ok {
			return "", false
		}
		return prefix + "." + e.Sel.Name, true
	}
	return "", false
}
