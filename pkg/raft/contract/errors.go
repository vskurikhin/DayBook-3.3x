package contract

import "errors"

// ErrRaftShutdown — ошибка, возвращаемая операциями остановленного узла Raft.
var ErrRaftShutdown = errors.New("raft: raft is shutdown")

// ErrEnqueueTimeout — ошибка истечения срока ожидания постановки операции в очередь.
var ErrEnqueueTimeout = errors.New("raft: timeout enqueuing operation")
