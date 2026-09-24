package raft

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"slices"
)

// ConfigurationChangeCommand — тип изменения конфигурации.
type ConfigurationChangeCommand int

const (
	AddVoter ConfigurationChangeCommand = iota + 1
	AddNonvoter
	DemoteVoter
	RemoveServer
)

// configurationChangeRequest — запрос на изменение конфигурации (внутренний).
type configurationChangeRequest struct {
	command       ConfigurationChangeCommand
	serverID      ServerID
	serverAddress ServerAddress
}

// configurations — пара committed+latest с индексами.
type configurations struct {
	committed      Configuration
	committedIndex int
	latest         Configuration
	latestIndex    int
}

// EncodeConfiguration кодирует конфигурацию в gob для записи в журнал.
func EncodeConfiguration(c Configuration) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeConfiguration декодирует конфигурацию из gob.
func DecodeConfiguration(data []byte) (Configuration, error) {
	var c Configuration
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)
	if err := dec.Decode(&c); err != nil {
		return c, err
	}
	return c, nil
}

// checkConfiguration проверяет конфигурацию на корректность.
func checkConfiguration(cfg Configuration) error {
	if len(cfg.ConfigServers) == 0 {
		return errors.New("raft: configuration has no servers")
	}
	seenID := make(map[ServerID]bool)
	seenAddr := make(map[ServerAddress]bool)
	hasVoter := false
	for _, s := range cfg.ConfigServers {
		if s.Address == "" {
			return fmt.Errorf("raft: server %d has empty address", s.ID)
		}
		if seenID[s.ID] {
			return fmt.Errorf("raft: duplicate server ID %d", s.ID)
		}
		seenID[s.ID] = true
		if seenAddr[s.Address] {
			return fmt.Errorf("raft: duplicate server address %s", s.Address)
		}
		seenAddr[s.Address] = true
		if s.Suffrage == Voter {
			hasVoter = true
		}
	}
	if !hasVoter {
		return errors.New("raft: configuration has no voters")
	}
	return nil
}

// nextConfiguration вычисляет новую конфигурацию на основе текущей и запроса.
func nextConfiguration(current Configuration, _ int, req configurationChangeRequest) (Configuration, error) {
	if err := checkConfiguration(current); err != nil {
		return Configuration{}, err
	}

	servers := slices.Clone(current.ConfigServers)

	switch req.command {
	case AddVoter:
		idx := findServer(servers, req.serverID)
		if idx >= 0 {
			servers[idx].Address = req.serverAddress
			if servers[idx].Suffrage == Nonvoter {
				servers[idx].Suffrage = Voter
			}
		} else {
			servers = append(servers, ConfigServer{
				ID:       req.serverID,
				Address:  req.serverAddress,
				Suffrage: Voter,
			})
		}

	case AddNonvoter:
		idx := findServer(servers, req.serverID)
		if idx >= 0 {
			servers[idx].Address = req.serverAddress
			if servers[idx].Suffrage == Voter {
				servers[idx].Suffrage = Nonvoter
			}
		} else {
			servers = append(servers, ConfigServer{
				ID:       req.serverID,
				Address:  req.serverAddress,
				Suffrage: Nonvoter,
			})
		}

	case DemoteVoter:
		idx := findServer(servers, req.serverID)
		if idx < 0 {
			return Configuration{}, fmt.Errorf("raft: server %d not found", req.serverID)
		}
		if servers[idx].Suffrage != Voter {
			return Configuration{}, fmt.Errorf("raft: server %d is not a voter", req.serverID)
		}
		servers[idx].Suffrage = Nonvoter
		if err := checkConfiguration(Configuration{ConfigServers: servers}); err != nil {
			return Configuration{}, err
		}

	case RemoveServer:
		idx := findServer(servers, req.serverID)
		if idx < 0 {
			return Configuration{}, fmt.Errorf("raft: server %d not found", req.serverID)
		}
		removed := servers[idx]
		servers = append(servers[:idx], servers[idx+1:]...)
		// self-removal разрешён, но последний голосующий не может быть удалён
		if err := checkConfiguration(Configuration{ConfigServers: servers}); err != nil {
			return Configuration{}, err
		}
		_ = removed

	default:
		return Configuration{}, fmt.Errorf("raft: unknown configuration change command %d", req.command)
	}

	result := Configuration{ConfigServers: servers}
	if err := checkConfiguration(result); err != nil {
		return Configuration{}, err
	}
	return result, nil
}

// voterIDs возвращает список ID голосующих из конфигурации.
func voterIDs(cfg Configuration) []int {
	var ids []int
	for _, s := range cfg.ConfigServers {
		if s.Suffrage == Voter {
			ids = append(ids, int(s.ID))
		}
	}
	return ids
}

// hasVote проверяет, является ли сервер с данным ID голосующим в конфигурации.
func hasVote(cfg Configuration, id int) bool {
	for _, s := range cfg.ConfigServers {
		if s.ID == ServerID(id) && s.Suffrage == Voter {
			return true
		}
	}
	return false
}

// findServer ищет сервер по ID. Возвращает индекс или -1.
func findServer(servers []ConfigServer, id ServerID) int {
	for i, s := range servers {
		if s.ID == id {
			return i
		}
	}
	return -1
}
