package raft

import (
	"strings"
	"testing"
	"time"
)

// TestValidateTiming_Boundaries проверяет, что каждая граница принимается
// ValidateTiming в составе явно указанной валидной четвёрки, а выход за границу
// отвергается с именем параметра.
func TestValidateTiming_Boundaries(t *testing.T) {
	valid := []struct {
		name string
		tc   TimerConfig
	}{
		{"defaults", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 20 * time.Millisecond, Reelection: 430 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}},
		{"pre-change defaults", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 21 * time.Millisecond, Reelection: 381 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}},
		{"MinHeartbeatTimeout", TimerConfig{Heartbeat: MinHeartbeatTimeout, Ticker: 20 * time.Millisecond, Reelection: 430 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}},
		{"MaxHeartbeatTimeout", TimerConfig{Heartbeat: MaxHeartbeatTimeout, Ticker: 21 * time.Millisecond, Reelection: 1000 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}},
		{"MinTickerTimeout", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: MinTickerTimeout, Reelection: 430 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}},
		{"MaxTickerTimeout", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: MaxTickerTimeout, Reelection: 10000 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}},
		{"MinReelectionTimeout", TimerConfig{Heartbeat: 5 * time.Millisecond, Ticker: 1 * time.Millisecond, Reelection: MinReelectionTimeout, ApplyBatch: 50 * time.Millisecond}},
		{"MaxReelectionTimeout", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 21 * time.Millisecond, Reelection: MaxReelectionTimeout, ApplyBatch: 50 * time.Millisecond}},
		{"MinApplyBatchInterval", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 20 * time.Millisecond, Reelection: 430 * time.Millisecond, ApplyBatch: MinApplyBatchInterval}},
		{"MaxApplyBatchInterval", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 21 * time.Millisecond, Reelection: 5000 * time.Millisecond, ApplyBatch: MaxApplyBatchInterval}},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateTiming(tc.tc); err != nil {
				t.Errorf("ValidateTiming(%v) = %v, want nil", tc.tc, err)
			}
		})
	}

	invalid := []struct {
		name          string
		tc            TimerConfig
		param         string
		wantSubstring string // дополнительная подстрока сообщения, если требуется
	}{
		{"heartbeat below min", TimerConfig{Heartbeat: 4 * time.Millisecond, Ticker: 20 * time.Millisecond, Reelection: 430 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}, "heartbeat-timeout", ""},
		{"heartbeat above max", TimerConfig{Heartbeat: 101 * time.Millisecond, Ticker: 21 * time.Millisecond, Reelection: 1100 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}, "heartbeat-timeout", "between 5ms and 100ms"},
		{"ticker below min", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 0, Reelection: 430 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}, "ticker-timeout", ""},
		{"ticker fractional", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 500 * time.Microsecond, Reelection: 430 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}, "ticker-timeout", ""},
		{"ticker above max", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 1001 * time.Millisecond, Reelection: 10000 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}, "ticker-timeout", ""},
		{"reelection below min", TimerConfig{Heartbeat: 5 * time.Millisecond, Ticker: 1 * time.Millisecond, Reelection: 199 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}, "reelection-timeout", ""},
		{"reelection above max", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 21 * time.Millisecond, Reelection: 30001 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}, "reelection-timeout", ""},
		{"apply below min", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 20 * time.Millisecond, Reelection: 430 * time.Millisecond, ApplyBatch: 0}, "apply-batch-interval", ""},
		{"apply above max", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 21 * time.Millisecond, Reelection: 5000 * time.Millisecond, ApplyBatch: 5001 * time.Millisecond}, "apply-batch-interval", ""},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTiming(tc.tc)
			if err == nil {
				t.Fatalf("ValidateTiming(%v) = nil, want error", tc.tc)
			}
			if !strings.Contains(err.Error(), tc.param) {
				t.Errorf("error %q does not mention parameter %q", err, tc.param)
			}
			if tc.wantSubstring != "" && !strings.Contains(err.Error(), tc.wantSubstring) {
				t.Errorf("error %q does not contain %q", err, tc.wantSubstring)
			}
		})
	}
}

// TestValidateTiming_CrossChecks проверяет межпараметрические нарушения
// C1, C2 и C4: ошибка называет оба участвующих параметра.
func TestValidateTiming_CrossChecks(t *testing.T) {
	cases := []struct {
		name   string
		tc     TimerConfig
		params []string
	}{
		{"C1", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 21 * time.Millisecond, Reelection: 300 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}, []string{"reelection-timeout", "heartbeat-timeout"}},
		{"C2", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 50 * time.Millisecond, Reelection: 430 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}, []string{"ticker-timeout", "reelection-timeout"}},
		{"C4", TimerConfig{Heartbeat: 33 * time.Millisecond, Ticker: 20 * time.Millisecond, Reelection: 430 * time.Millisecond, ApplyBatch: 500 * time.Millisecond}, []string{"apply-batch-interval", "reelection-timeout"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTiming(tc.tc)
			if err == nil {
				t.Fatalf("ValidateTiming(%v) = nil, want error", tc.tc)
			}
			for _, p := range tc.params {
				if !strings.Contains(err.Error(), p) {
					t.Errorf("error %q does not mention parameter %q", err, p)
				}
			}
		})
	}
}

// TestValidateTiming_AllViolationsTogether проверяет, что несколько
// нарушений возвращаются одной ошибкой (errors.Join) и все присутствуют
// в её тексте.
func TestValidateTiming_AllViolationsTogether(t *testing.T) {
	tc := TimerConfig{Heartbeat: 4 * time.Millisecond, Ticker: 1001 * time.Millisecond, Reelection: 199 * time.Millisecond, ApplyBatch: 5001 * time.Millisecond}
	err := ValidateTiming(tc)
	if err == nil {
		t.Fatalf("ValidateTiming(%v) = nil, want error", tc)
	}
	for _, p := range []string{"heartbeat-timeout", "ticker-timeout", "reelection-timeout", "apply-batch-interval"} {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("error %q does not mention parameter %q", err, p)
		}
	}
}

// TestValidateTiming_FractionalMilliseconds проверяет отказ дробного
// значения с требованием целого числа миллисекунд.
func TestValidateTiming_FractionalMilliseconds(t *testing.T) {
	err := ValidateTiming(TimerConfig{Heartbeat: 30 * time.Millisecond, Ticker: 500 * time.Microsecond, Reelection: 430 * time.Millisecond, ApplyBatch: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("ValidateTiming = nil, want error for fractional milliseconds")
	}
	if !strings.Contains(err.Error(), "ticker-timeout") {
		t.Errorf("error %q does not mention ticker-timeout", err)
	}
}

// TestValidateTiming_MessageHeartbeat проверяет текст сообщения B1 для
// входа heartbeat=200ms: содержит имя параметра и значение 100 (не 100.5).
func TestValidateTiming_MessageHeartbeat(t *testing.T) {
	err := ValidateTiming(TimerConfig{Heartbeat: 200 * time.Millisecond, Ticker: 20 * time.Millisecond, Reelection: 430 * time.Millisecond, ApplyBatch: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("ValidateTiming = nil, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "heartbeat-timeout") {
		t.Errorf("message %q does not mention heartbeat-timeout", msg)
	}
	if !strings.Contains(msg, "100") {
		t.Errorf("message %q does not mention 100", msg)
	}
	if strings.Contains(msg, "100.5") {
		t.Errorf("message %q must not advertise 100.5", msg)
	}
}

// TestValidateTiming_MaxHeartbeatTimeoutIntegral проверяет, что верхняя
// граница пульса — целое число миллисекунд и равна 100 мс.
func TestValidateTiming_MaxHeartbeatTimeoutIntegral(t *testing.T) {
	if MaxHeartbeatTimeout%time.Millisecond != 0 {
		t.Errorf("MaxHeartbeatTimeout = %v, want integral milliseconds", MaxHeartbeatTimeout)
	}
	if MaxHeartbeatTimeout != 100*time.Millisecond {
		t.Errorf("MaxHeartbeatTimeout = %v, want 100ms", MaxHeartbeatTimeout)
	}
}

// TestValidateTiming_NoPermutation проверяет, что TimerConfig передаётся
// именованными полями: перестановка одноимённых по типу аргументов
// невозможна. Прямой тест перестановки на функции не нужен —
// сигнатура принимает структуру.
func TestValidateTiming_NoPermutation(t *testing.T) {
	swapped := TimerConfig{Heartbeat: 21 * time.Millisecond, Ticker: 33 * time.Millisecond, Reelection: 430 * time.Millisecond, ApplyBatch: 50 * time.Millisecond}
	if err := ValidateTiming(swapped); err != nil {
		t.Errorf("ValidateTiming with swapped heartbeat/ticker = %v, want nil (fields are named)", err)
	}
}
