package tracelog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// tracePrefixFormatLiteral — единственная production-строка формата
// обязательного префикса трассировки. Проверяется как единый источник:
// вторая копия формата в пакете недопустима.
const tracePrefixFormatLiteral = "[%c,N:%d,T:%03d] "

// TestTracePrefix проверяет табличный контракт формата префикса:
// буква состояния (F/L/C и отметка «?» для неизвестного состояния),
// идентификатор узла 0/1/99 и терм типа int, включая значение больше 999.
func TestTracePrefix(t *testing.T) {
	tests := []struct {
		name   string
		letter rune
		id     int
		term   int
		want   string
	}{
		{"follower, нули", 'F', 0, 0, "[F,N:0,T:000] "},
		{"follower, терм 7", 'F', 1, 7, "[F,N:1,T:007] "},
		{"candidate, id и терм 99", 'C', 99, 99, "[C,N:99,T:099] "},
		{"leader, терм больше 999", 'L', 1, 1000, "[L,N:1,T:1000] "},
		{"состояние без буквы", '?', 1, 7, "[?,N:1,T:007] "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatPrefix(Prefix{Letter: tc.letter, ID: tc.id, Term: tc.term})
			if got != tc.want {
				t.Fatalf("FormatPrefix(%q, %d, %d) = %q, ожидалось %q",
					tc.letter, tc.id, tc.term, got, tc.want)
			}
		})
	}
}

// TestTracePrefixSingleProductionFormat проверяет, что строка формата
// префикса встречается в production-файлах пакета ровно один раз:
// единый источник формата, дубликат обязан провалить тест.
func TestTracePrefixSingleProductionFormat(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("список файлов пакета: %v", err)
	}
	total := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("чтение %s: %v", file, err)
		}
		count := strings.Count(string(src), tracePrefixFormatLiteral)
		if count > 0 {
			t.Logf("строка формата префикса: %s, вхождений %d", file, count)
		}
		total += count
	}
	if total != 1 {
		t.Fatalf("строка формата %q встречается в production %d раз(а), ожидалось 1",
			tracePrefixFormatLiteral, total)
	}
}

// tracePrefixCorpusPattern — разбор строки трассировки уровня 5 из
// снимка стенда: метка с микросекундами, обязательный префикс и начало
// тела. Терм принимается из трёх и более цифр: тип int не ограничен
// сотнями.
var tracePrefixCorpusPattern = regexp.MustCompile(
	`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\.\d{6} \[[FLC?],N:\d+,T:\d{3,}\] `)

// tracePrefixCorpusTermPattern — вспомогательный образец поля терма.
var tracePrefixCorpusTermPattern = regexp.MustCompile(`T:\d{3,}`)

// traceCorpusLine — реальная строка снимка стенда уровня 5.
//
// Источник: data/reports/2026-09-15.P-PERF-2-stage1/debug-l5_raftcm-1.trace:1
// (первая строка файла узла 1). Строка встроена в тест, чтобы проверка
// не зависела от игнорируемого git каталога данных.
const traceCorpusLine = `2026/09/15 11:19:37.830656 [F,N:1,T:000] election timer started (712ms), term=0`

// TestTracePrefixCorpusLine сверяет формат с реальной строкой снимка:
// метка, префикс и терм из трёх цифр; проверяет, что терм больше 999
// печатается полностью, а двузначный терм образцом не принимается.
func TestTracePrefixCorpusLine(t *testing.T) {
	if !tracePrefixCorpusPattern.MatchString(traceCorpusLine) {
		t.Fatalf("строка снимка не соответствует формату: %q", traceCorpusLine)
	}
	if !tracePrefixCorpusTermPattern.MatchString(traceCorpusLine) {
		t.Fatalf("строка снимка не содержит поле терма T:<три и более цифр>: %q", traceCorpusLine)
	}

	// Строка-эталон побайтово воспроизводится правилами контракта:
	// метка снимка уже с микросекундами, тело сохранено как есть.
	label := traceCorpusLine[:len(traceTimeLayout)]
	at, err := time.ParseInLocation(traceTimeLayout, label, time.Local)
	if err != nil {
		t.Fatalf("разбор метки строки снимка: %v", err)
	}
	if got := fullTraceTestLine(at, 'F', 1, 0, "election timer started (712ms), term=0"); got != traceCorpusLine+"\n" {
		t.Fatalf("воспроизведение строки снимка = %q, ожидалось %q", got, traceCorpusLine+"\n")
	}

	// Терм больше 999 не усекается: образец принимает четыре цифры.
	longLine := fullTraceTestLine(at, 'L', 99, 1000, "line")
	if !tracePrefixCorpusPattern.MatchString(longLine) {
		t.Fatalf("строка с термом 1000 не соответствует образцу: %q", longLine)
	}
	if !strings.Contains(longLine, "T:1000") {
		t.Fatalf("терм 1000 напечатан не полностью: %q", longLine)
	}

	// Образец требует не менее трёх цифр: строка с двузначным термом
	// (формат всегда дополняет его нулём, поэтому проверяется прямая
	// строка) отвергается.
	shortLine := "2026/09/15 11:19:37.830656 [L,N:1,T:99] line\n"
	if tracePrefixCorpusPattern.MatchString(shortLine) {
		t.Fatalf("образец принял двузначный терм: %q", shortLine)
	}
}

// TestTracePrefixLineLabel проверяет метку полной строки побайтово:
// прямые ожидаемые строки и независимый оракул time.Time.Format;
// часовой пояс отличается от UTC, микросекунды усекаются (7000 нс и
// 999999999 нс), проверяются границы суток и года.
func TestTracePrefixLineLabel(t *testing.T) {
	zonePlus3 := time.FixedZone("test+03", 3*60*60)
	zoneMinus5 := time.FixedZone("test-05", -5*60*60)
	if zonePlus3 == time.UTC || zoneMinus5 == time.UTC {
		t.Fatal("тестовые пояса не должны совпадать с UTC")
	}
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{
			name: "микросекунды 000007, пояс +03:00",
			at:   time.Date(2026, 9, 15, 11, 19, 37, 7000, zonePlus3),
			want: "2026/09/15 11:19:37.000007",
		},
		{
			name: "наносекунды 999999999 усекаются, пояс −05:00",
			at:   time.Date(2026, 9, 15, 23, 59, 59, 999999999, zoneMinus5),
			want: "2026/09/15 23:59:59.999999",
		},
		{
			name: "граница суток и года",
			at:   time.Date(2025, 12, 31, 23, 59, 59, 999999999, time.UTC),
			want: "2025/12/31 23:59:59.999999",
		},
		{
			name: "начало года",
			at:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			want: "2026/01/01 00:00:00.000000",
		},
		{
			name: "1999 нс усекаются до 000001",
			at:   time.Date(2026, 6, 30, 12, 0, 0, 1999, time.UTC),
			want: "2026/06/30 12:00:00.000001",
		},
		{
			name: "999 нс усекаются до 000000",
			at:   time.Date(2026, 6, 30, 12, 0, 0, 999, time.UTC),
			want: "2026/06/30 12:00:00.000000",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Независимый оракул стандартной библиотеки.
			if got := tc.at.Format(traceTimeLayout); got != tc.want {
				t.Fatalf("оракул Format = %q, ожидалось %q", got, tc.want)
			}
			// Побайтовое совпадение полной строки: метка, префикс, тело.
			line := writeSingleTraceLine(t, tc.at, 'F', 1, 7, "line")
			want := tc.want + " " + FormatPrefix(Prefix{Letter: 'F', ID: 1, Term: 7}) + "line\n"
			if line != want {
				t.Fatalf("строка = %q, ожидалось %q", line, want)
			}
		})
	}
}

// TestTracePrefixNewlineRules проверяет правило перевода строки побайтово:
// пустое тело и тело без перевода строки получают ровно один \n; тела с
// одним и двумя \n сохраняются без изменений.
func TestTracePrefixNewlineRules(t *testing.T) {
	at := time.Date(2026, 9, 15, 11, 19, 37, 7000, time.UTC)
	tests := []struct {
		name string
		body string
		want string
	}{
		{"пустое тело", "", "2026/09/15 11:19:37.000007 [F,N:1,T:007] \n"},
		{"без перевода строки", "body", "2026/09/15 11:19:37.000007 [F,N:1,T:007] body\n"},
		{"один перевод строки", "body\n", "2026/09/15 11:19:37.000007 [F,N:1,T:007] body\n"},
		{"два перевода строки", "body\n\n", "2026/09/15 11:19:37.000007 [F,N:1,T:007] body\n\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := writeSingleTraceLine(t, at, 'F', 1, 7, tc.body); got != tc.want {
				t.Fatalf("строка = %q, ожидалось %q", got, tc.want)
			}
		})
	}
}

// writeSingleTraceLine записывает одну готовую строку через отдельный
// писатель с фиксированными часами и возвращает её побайтово.
func writeSingleTraceLine(t *testing.T, at time.Time, letter rune, id, term int, body string) string {
	t.Helper()
	sink := &traceTestSink{}
	w := startTraceTestWriter(t, sink, 8, func() time.Time { return at })
	w.EnqueueText(Prefix{Letter: letter, ID: id, Term: term}, body)
	lines := flushTraceTest(t, w, sink)
	if len(lines) != 1 {
		t.Fatalf("записано %d строк, ожидалась 1: %q", len(lines), lines)
	}
	return lines[0]
}
