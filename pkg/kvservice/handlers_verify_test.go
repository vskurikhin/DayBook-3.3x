package kvservice

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// verifyLeaderCallsIn возвращает число вызовов VerifyLeader в теле функции
// с именем name из файла kvservice.go.
func verifyLeaderCallsIn(t *testing.T, name string) int {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "kvservice.go", nil, 0)
	if err != nil {
		t.Fatalf("parse kvservice.go: %v", err)
	}

	var target *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			target = fn
			break
		}
	}
	if target == nil {
		t.Fatalf("function %s not found in kvservice.go", name)
	}

	calls := 0
	ast.Inspect(target.Body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "VerifyLeader" {
			calls++
		}
		return true
	})
	return calls
}

// TestWriteHandlersDoNotVerifyLeader — AC-3: запись не выполняет
// предваряющего кворумного раунда подтверждения лидерства. Фиксация
// команды сама доказывает лидерство, а ограничение ущерба при потере
// кворума обеспечивает шаг лидера вниз в консенсус-модуле.
//
// Проверка структурная: тип поля rs — конкретный *raft.Server, подставить
// счётчик вызовов в тесте пакета нельзя, а изменять корневой пакет raft
// в этой задаче запрещено.
func TestWriteHandlersDoNotVerifyLeader(t *testing.T) {
	for _, name := range []string{"handlePut", "handleCAS"} {
		if got := verifyLeaderCallsIn(t, name); got != 0 {
			t.Errorf("%s calls VerifyLeader %d time(s), want 0", name, got)
		}
	}
}

// TestReadHandlersStillVerifyLeader — контроль от вырождения предыдущей
// проверки: подтверждение лидерства для чтений и отдельной ручки
// сохраняется в прежнем виде.
func TestReadHandlersStillVerifyLeader(t *testing.T) {
	for _, name := range []string{"handleGet", "handleVerifyLeader", "handleWeakGet"} {
		if got := verifyLeaderCallsIn(t, name); got == 0 {
			t.Errorf("%s does not call VerifyLeader, want it preserved", name)
		}
	}
}
