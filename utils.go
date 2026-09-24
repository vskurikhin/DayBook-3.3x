package raft

import (
	"crypto/rand"
	"math/big"
	"reflect"
)

// IsNilInterface возвращает true, если v — nil-интерфейс или интерфейс,
// содержащий типизированный nil (nil-указатель, nil-канал, nil-map и т.п.).
//
// В Go сравнение интерфейса с nil (v == nil) ловит только «чистый» nil —
// когда тип и значение интерфейса пусты. Если в интерфейс положен
// типизированный nil (например, (*transp.InmemTransport)(nil)), интерфейс НЕ равен
// nil: у него есть тип, а значением является nil-указатель. Прямое сравнение
// такую ситуацию пропустит, что позже приведёт к nil-dereference в месте
// использования. Единственный способ обнаружить типизированный nil без
// изменения сигнатуры — рефлексия.
func IsNilInterface(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	}
	return false
}

// RandomInt - равномерное распределение в диапазоне (удобная обертка).
func RandomInt(m int64) (int64, error) {
	n := big.NewInt(m)
	v, err := rand.Int(rand.Reader, n)
	if err != nil {
		return 0, err
	}
	return v.Int64(), nil
}
