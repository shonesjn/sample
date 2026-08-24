package clientif

import (
	"errors"
	"log/slog"
	"sync"
)

type ClientsManager struct {
	Clients   map[string]*Client
	ClientsMu sync.RWMutex
}

func NewClientsManager() *ClientsManager {
	return &ClientsManager{
		Clients: make(map[string]*Client),
	}
}

func (cm *ClientsManager) CreateClient(userID string, protocol string) (*Client, error) {
	cm.ClientsMu.Lock()
	defer cm.ClientsMu.Unlock()

	if !IsValidProtocol(protocol) {
		return nil, errors.New("invalid protocol")
	}

	if existing, exists := cm.Clients[userID]; exists {
		slog.Warn("Client already exists, cleaning up old connection", "userID", userID)
		existing.Close()
		delete(cm.Clients, userID)
	}

	client := &Client{
		UserID:   userID,
		Protocol: protocol,
	}
	cm.Clients[userID] = client
	slog.Info("Client created", "userID", userID)
	return client, nil
}

func (cm *ClientsManager) GetClient(userID string) (*Client, bool) {
	cm.ClientsMu.RLock()
	defer cm.ClientsMu.RUnlock()
	client, exists := cm.Clients[userID]
	return client, exists
}

func (cm *ClientsManager) RemoveClient(userID string) {
	cm.ClientsMu.Lock()
	defer cm.ClientsMu.Unlock()

	client, exists := cm.Clients[userID]
	if !exists {
		return
	}

	// Graceful cleanup of all connections
	client.Close()
	delete(cm.Clients, userID)
	slog.Info("Client removed", "userID", userID)
}

func (cm *ClientsManager) CloseAll() {
	cm.ClientsMu.Lock()
	defer cm.ClientsMu.Unlock()

	for userID, client := range cm.Clients {
		client.Close()
		delete(cm.Clients, userID)
		slog.Info("Client closed during shutdown", "userID", userID)
	}
}
