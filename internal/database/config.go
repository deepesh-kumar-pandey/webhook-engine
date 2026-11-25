package database

import "time"

type Config struct {
	Path                string
	MaxReadConnections  int
	MaxWriteConnections int
	BusyTimeout         time.Duration
}
