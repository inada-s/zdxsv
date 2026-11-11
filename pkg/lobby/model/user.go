package model

import (
	"zdxsv/pkg/db"
)

const (
	EntryNone   = 0
	EntryAeug   = 1
	EntryTitans = 2
)

type LbsUser struct {
	db.DBUser
	Entry byte
	Bin   string
	// Sync is the emulator's sync fingerprint: lobby battles start only among equal ones.
	Sync string

	Room   *LbsRoom
	Lobby  *LbsLobby
	Battle *LbsBattle
}
