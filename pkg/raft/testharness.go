package raft

import (
	"log"
	"sync"
	"testing"
	"time"
)

func init() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
}

type Harness struct {
	mu sync.Mutex

	// cluster — список всех серверов Raft, участвующих в кластере.
	cluster []*Server

	// commitChans содержит по одному каналу фиксации для каждого
	// сервера кластера.
	commitChans []chan CommitEntry

	// commits с индексом i содержит последовательность записей,
	// зафиксированных сервером i на текущий момент.
	// Этот массив заполняется горутинами, прослушивающими
	// соответствующие каналы фиксации.
	commits [][]CommitEntry

	// connected содержит по одному логическому значению для каждого сервера
	// кластера и определяет, подключён ли данный сервер в настоящий момент
	// к соседям (если false, сервер изолирован, и никакие сообщения
	// не передаются ни к нему, ни от него).
	connected []bool

	n int
	t *testing.T
}

// NewHarness создаёт новый тестовый запряжку,
// инициализированную n серверами, соединёнными друг с другом.
func NewHarness(t *testing.T, n int) *Harness {
	ns := make([]*Server, n)
	connected := make([]bool, n)
	commitChans := make([]chan CommitEntry, n)
	commits := make([][]CommitEntry, n)
	ready := make(chan any)

	// Создать все серверы этого кластера, назначить им идентификаторы
	// и идентификаторы соседей.
	for i := 0; i < n; i++ {
		peerIds := make([]int, 0)
		for p := 0; p < n; p++ {
			if p != i {
				peerIds = append(peerIds, p)
			}
		}

		commitChans[i] = make(chan CommitEntry)
		ns[i] = NewServer(i, peerIds, ready, commitChans[i])
		ns[i].Serve(":0")
	}

	// Соединить всех соседей друг с другом.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j {
				ns[i].ConnectToPeer(j, ns[j].GetListenAddr())
			}
		}
		connected[i] = true
	}
	close(ready)

	h := &Harness{
		cluster:     ns,
		commitChans: commitChans,
		commits:     commits,
		connected:   connected,
		n:           n,
		t:           t,
	}
	for i := 0; i < n; i++ {
		go h.collectCommits(i)
	}
	return h
}

// Shutdown останавливает все серверы в тестовом окружении и ожидает,
// пока они полностью завершат работу.
func (h *Harness) Shutdown() {
	for i := 0; i < h.n; i++ {
		h.cluster[i].DisconnectAll()
		h.connected[i] = false
	}
	for i := 0; i < h.n; i++ {
		h.cluster[i].Shutdown()
	}
	for i := 0; i < h.n; i++ {
		close(h.commitChans[i])
	}
}

// DisconnectPeer отключает сервер от всех остальных серверов кластера.
func (h *Harness) DisconnectPeer(id int) {
	tLogf("Disconnect %d", id)
	h.cluster[id].DisconnectAll()
	for j := 0; j < h.n; j++ {
		if j != id {
			h.cluster[j].DisconnectPeer(id)
		}
	}
	h.connected[id] = false
}

// ReconnectPeer повторно подключает сервер ко всем остальным серверам кластера.
func (h *Harness) ReconnectPeer(id int) {
	tLogf("Reconnect %d", id)
	for j := 0; j < h.n; j++ {
		if j != id {
			if err := h.cluster[id].ConnectToPeer(j, h.cluster[j].GetListenAddr()); err != nil {
				h.t.Fatal(err)
			}
			if err := h.cluster[j].ConnectToPeer(id, h.cluster[id].GetListenAddr()); err != nil {
				h.t.Fatal(err)
			}
		}
	}
	h.connected[id] = true
}

// CheckSingleLeader проверяет, что только один сервер считает себя лидером.
// Возвращает идентификатор лидера и текущий терм. Если лидер ещё не
// определён, выполняет несколько повторных попыток проверки.
func (h *Harness) CheckSingleLeader() (int, int) {
	for r := 0; r < 8; r++ {
		leaderId := -1
		leaderTerm := -1
		for i := 0; i < h.n; i++ {
			if h.connected[i] {
				_, term, isLeader := h.cluster[i].cm.Report()
				if isLeader {
					if leaderId < 0 {
						leaderId = i
						leaderTerm = term
					} else {
						h.t.Fatalf("both %d and %d think they're leaders", leaderId, i)
					}
				}
			}
		}
		if leaderId >= 0 {
			return leaderId, leaderTerm
		}
		time.Sleep(150 * time.Millisecond)
	}

	h.t.Fatalf("leader not found")
	return -1, -1
}

// CheckNoLeader проверяет, что ни один из подключённых серверов
// не считает себя лидером.
func (h *Harness) CheckNoLeader() {
	for i := 0; i < h.n; i++ {
		if h.connected[i] {
			_, _, isLeader := h.cluster[i].cm.Report()
			if isLeader {
				h.t.Fatalf("server %d leader; want none", i)
			}
		}
	}
}

// CheckCommitted проверяет, что команда cmd зафиксирована на всех
// подключённых серверах с одинаковым индексом. Также проверяется,
// что все команды, зафиксированные *до* команды cmd, совпадают.
// Для корректной работы все команды, отправляемые в Raft,
// должны быть уникальными положительными целыми числами.
// Возвращает количество серверов, на которых команда зафиксирована,
// и индекс этой записи журнала.
func (h *Harness) CheckCommitted(cmd int) (nc int, index int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Определить длину среза commits для подключённых серверов.
	commitsLen := -1
	for i := 0; i < h.n; i++ {
		if h.connected[i] {
			if commitsLen >= 0 {
				// If this was set already, expect the new length to be the same.
				if len(h.commits[i]) != commitsLen {
					h.t.Fatalf("commits[%d] = %d, commitsLen = %d", i, h.commits[i], commitsLen)
				}
			} else {
				commitsLen = len(h.commits[i])
			}
		}
	}

	// Проверить согласованность зафиксированных команд, начиная с первой
	// и до команды, которую требуется найти. Цикл завершится сразу после
	// обнаружения команды cmd.
	for c := 0; c < commitsLen; c++ {
		cmdAtC := -1
		for i := 0; i < h.n; i++ {
			if h.connected[i] {
				cmdOfN := h.commits[i][c].Command.(int)
				if cmdAtC >= 0 {
					if cmdOfN != cmdAtC {
						h.t.Errorf("got %d, want %d at h.commits[%d][%d]", cmdOfN, cmdAtC, i, c)
					}
				} else {
					cmdAtC = cmdOfN
				}
			}
		}
		if cmdAtC == cmd {
			// Проверить согласованность значения Index.
			index := -1
			nc := 0
			for i := 0; i < h.n; i++ {
				if h.connected[i] {
					if index >= 0 && h.commits[i][c].Index != index {
						h.t.Errorf("got Index=%d, want %d at h.commits[%d][%d]", h.commits[i][c].Index, index, i, c)
					} else {
						index = h.commits[i][c].Index
					}
					nc++
				}
			}
			return nc, index
		}
	}

	// Если не произошло досрочного выхода, значит требуемая команда
	// не была найдена среди зафиксированных.
	h.t.Errorf("cmd=%d not found in commits", cmd)
	return -1, -1
}

// CheckCommittedN проверяет, что команда cmd была зафиксирована
// ровно на n подключённых серверах.
func (h *Harness) CheckCommittedN(cmd int, n int) {
	nc, _ := h.CheckCommitted(cmd)
	if nc != n {
		h.t.Errorf("CheckCommittedN got nc=%d, want %d", nc, n)
	}
}

// CheckNotCommitted проверяет, что ни на одном из активных серверов
// команда cmd ещё не была зафиксирована.
func (h *Harness) CheckNotCommitted(cmd int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for i := 0; i < h.n; i++ {
		if h.connected[i] {
			for c := 0; c < len(h.commits[i]); c++ {
				gotCmd := h.commits[i][c].Command.(int)
				if gotCmd == cmd {
					h.t.Errorf("found %d at commits[%d][%d], expected none", cmd, i, c)
				}
			}
		}
	}
}

// SubmitToServer отправляет команду серверу с идентификатором serverId.
func (h *Harness) SubmitToServer(serverId int, cmd any) bool {
	return h.cluster[serverId].cm.Submit(cmd)
}

func tLogf(format string, a ...any) {
	format = "[TEST] " + format
	log.Printf(format, a...)
}

func sleepMs(n int) {
	time.Sleep(time.Duration(n) * time.Millisecond)
}

// collectCommits читает сообщения из канала commitChans[i] и добавляет
// все полученные записи в соответствующий commits[i]. Метод является
// блокирующим и должен выполняться в отдельной горутине.
// Завершается после закрытия канала commitChans[i].
func (h *Harness) collectCommits(i int) {
	for c := range h.commitChans[i] {
		h.mu.Lock()
		tLogf("collectCommits(%d) got %+v", i, c)
		h.commits[i] = append(h.commits[i], c)
		h.mu.Unlock()
	}
}
