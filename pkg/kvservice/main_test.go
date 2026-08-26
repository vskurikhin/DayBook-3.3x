package kvservice

import (
	"os"
	"testing"
)

// TestMain выполняет единственную статическую конфигурацию.
func TestMain(m *testing.M) {
	if err := SetTrace(TraceConfig{}); err != nil {
		_, _ = os.Stderr.WriteString("kvservice: trace configuration failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}
