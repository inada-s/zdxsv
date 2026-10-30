package model

import (
	"zdxsv/pkg/db"
)

const (
	EntryNone   = 0
	EntryAeug   = 1
	EntryTitans = 2
)

type User struct {
	db.User
	Entry byte
	Bin   string
	// Sync is the emulator's sync fingerprint: lobby battles start only among equal ones.
	Sync string

	Room   *Room
	Lobby  *Lobby
	Battle *Battle
}
