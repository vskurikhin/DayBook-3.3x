package config

import (
	"os"
	"testing"
	"time"
)

// withArgs подменяет аргументы командной строки на время теста.
func withArgs(t *testing.T, args ...string) {
	t.Helper()
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })
	os.Args = append([]string{"loadkv"}, args...)
}

// TestParseFlagsDefaults проверяет значения по умолчанию, в том числе для
// флагов конкурентности, длительности и размера значения.
func TestParseFlagsDefaults(t *testing.T) {
	withArgs(t)

	v := ParseFlags()

	if v.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4", v.Concurrency)
	}
	if v.Duration != 0 {
		t.Errorf("Duration = %v, want 0", v.Duration)
	}
	if v.ValueSize != 128 {
		t.Errorf("ValueSize = %d, want 128", v.ValueSize)
	}
	if v.GetPercent != 66 {
		t.Errorf("GetPercent = %d, want 66", v.GetPercent)
	}
	if v.KeyCount != 2000 {
		t.Errorf("KeyCount = %d, want 2000", v.KeyCount)
	}
	if v.RequestRate != 100 {
		t.Errorf("RequestRate = %d, want 100", v.RequestRate)
	}
	if v.VerifyPercent != 33 {
		t.Errorf("VerifyPercent = %d, want 33", v.VerifyPercent)
	}
	if len(v.Peers) != 0 {
		t.Errorf("len(Peers) = %d, want 0", len(v.Peers))
	}
}

// TestParseFlagsExplicit проверяет разбор явно заданных значений всех флагов.
func TestParseFlagsExplicit(t *testing.T) {
	withArgs(t,
		"-concurrency", "64",
		"-duration", "5m",
		"-get-percent", "75",
		"-key-count", "10",
		"-request-rate", "100000",
		"-peers", ":8881,:8882",
		"-value-size", "1024",
		"-verify-percent", "10",
	)

	v := ParseFlags()

	if v.Concurrency != 64 {
		t.Errorf("Concurrency = %d, want 64", v.Concurrency)
	}
	if v.Duration != 5*time.Minute {
		t.Errorf("Duration = %v, want 5m", v.Duration)
	}
	if v.GetPercent != 75 {
		t.Errorf("GetPercent = %d, want 75", v.GetPercent)
	}
	if v.KeyCount != 10 {
		t.Errorf("KeyCount = %d, want 10", v.KeyCount)
	}
	if v.RequestRate != 100000 {
		t.Errorf("RequestRate = %d, want 100000", v.RequestRate)
	}
	if v.ValueSize != 1024 {
		t.Errorf("ValueSize = %d, want 1024", v.ValueSize)
	}
	if v.VerifyPercent != 10 {
		t.Errorf("VerifyPercent = %d, want 10", v.VerifyPercent)
	}
	if len(v.Peers) != 2 {
		t.Fatalf("len(Peers) = %d, want 2", len(v.Peers))
	}
}

// TestParseFlagsPeersWithScheme проверяет разбор адресов со схемой и без неё.
func TestParseFlagsPeersWithScheme(t *testing.T) {
	withArgs(t, "-peers", "http://127.0.0.1:8881, 127.0.0.1:8882")

	v := ParseFlags()

	if len(v.Peers) != 2 {
		t.Fatalf("len(Peers) = %d, want 2", len(v.Peers))
	}
	if v.Peers[0].String() != "127.0.0.1:8881" {
		t.Errorf("Peers[0] = %s, want 127.0.0.1:8881", v.Peers[0].String())
	}
	if v.Peers[1].String() != "127.0.0.1:8882" {
		t.Errorf("Peers[1] = %s, want 127.0.0.1:8882", v.Peers[1].String())
	}
}
