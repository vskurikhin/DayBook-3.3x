package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// traceSubprocessStartup — предельное время ожидания успешного старта
// дочернего узла (открытие HTTP-порта).
const traceSubprocessStartup = 10 * time.Second

// traceSubprocessShutdown — предельное время штатного завершения узла
// после SIGTERM.
const traceSubprocessShutdown = 10 * time.Second

// traceSubprocessPoll — интервал опроса готовности HTTP-порта.
const traceSubprocessPoll = 20 * time.Millisecond

// traceStderr — потокобезопасный приёмник потока ошибок дочернего
// процесса: os/exec пишет из отдельной горутины, тест читает при
// диагностике.
type traceStderr struct {
	mu sync.Mutex
	b  bytes.Buffer
}

// Write принимает порцию вывода процесса.
func (s *traceStderr) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// String возвращает накопленный вывод.
func (s *traceStderr) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestTraceSigtermShutdown собирает исполняемый файл узла, запускает его
// без соседей с включённой трассировкой в файл, дожидается успешного
// старта, посылает SIGTERM и проверяет штатный останов: код возврата 0
// и записанный конечный хвост трассы со строкой CM.Stop.
func TestTraceSigtermShutdown(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "raftkv")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("сборка raftkv: %v\n%s", err, out)
	}

	httpAddr := freeTCPAddr(t)
	rpcAddr := freeTCPAddr(t)
	tracePath := filepath.Join(dir, "cm.trace")
	proc := exec.Command(binary,
		"-number=1",
		"-http-addr="+httpAddr,
		"-rpc-addr="+rpcAddr,
		"-data-dir="+filepath.Join(dir, "data"),
		"-trace-log-level=1",
		"-trace-cm-log-file="+tracePath,
	)
	var stderr traceStderr
	proc.Stdout = io.Discard
	proc.Stderr = &stderr
	if err := proc.Start(); err != nil {
		t.Fatalf("запуск raftkv: %v", err)
	}
	t.Cleanup(func() {
		if proc.ProcessState == nil {
			_ = proc.Process.Kill()
			_, _ = proc.Process.Wait()
		}
	})

	// Успешный старт: HTTP-порт принимает соединения.
	waitTCPReady(t, httpAddr, traceSubprocessStartup)

	if err := proc.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("узел завершился с ошибкой: %v\nstderr: %s", err, stderr.String())
		}
	case <-time.After(traceSubprocessShutdown):
		_ = proc.Process.Kill()
		t.Fatalf("узел не завершился после SIGTERM за %v\nstderr: %s", traceSubprocessShutdown, stderr.String())
	}

	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("чтение файла трассы: %v", err)
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		t.Fatal("файл трассы пуст: сигнал не довёл хвост до записи")
	}
	lines := strings.Split(text, "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "CM.Stop called / becomes Dead") {
		t.Fatalf("конечный хвост трассы %q не содержит строку останова CM.Stop", last)
	}
}

// freeTCPAddr возвращает свободный адрес 127.0.0.1: случайный порт,
// слушатель немедленно закрывается.
func freeTCPAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("поиск свободного порта: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("закрытие слушателя: %v", err)
	}
	return addr
}

// waitTCPReady ожидает открытия TCP-порта с ограничением времени.
func waitTCPReady(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("HTTP-порт %s не открылся за %v", addr, timeout)
		}
		time.Sleep(traceSubprocessPoll)
	}
}
