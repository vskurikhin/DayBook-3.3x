package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/mitchellh/go-ps"
)

var oldArgs = os.Args

func TestWithAddressArgs(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			_, _ = fmt.Fprintf(os.Stderr, "recover: %v\n", r)
			if fmt.Sprintf("%v", r) != "unexpected call to os.Exit(0) during test" {
				t.Fatal(r)
			}
		}
	}()
	oldArgs = os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"main", "run", "--address", freeAddr(t)}
	go func() {
		time.Sleep(1 * time.Second)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	}()
	main()
}

func TestMainLogicExitCode(t *testing.T) {
	var cmd *exec.Cmd
	done := make(chan struct{})
	go func() {
		cmd = exec.Command("go", "run", "main.go", "run", "--address", freeAddr(t))
		output, _ := cmd.CombinedOutput()
		_, _ = fmt.Fprintf(os.Stderr, "output: %s\n", string(output))
		close(done)
	}()
	time.Sleep(900 * time.Millisecond)
	_, _ = fmt.Fprintf(os.Stderr, "cmd.Process.Pid: %d\n", cmd.Process.Pid)
	parentPID := cmd.Process.Pid
	time.Sleep(100 * time.Millisecond)

	// Получаем все процессы
	processList, err := ps.Processes()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Printf("Child processes of PID %d:\n", parentPID)
	for _, proc := range processList {
		// proc.PPid() возвращает родительский PID
		if proc.PPid() == parentPID {
			fmt.Printf("- PID: %d, Name: %s\n", proc.Pid(), proc.Executable())
			_ = syscall.Kill(proc.Pid(), syscall.SIGINT)
		}
	}

	select {
	case <-done:
		// success
	case <-time.After(12 * time.Second):
		t.Fatal("server did not exit after signal")
	}
}

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}
