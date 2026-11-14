package main

import (
	"context"
	"net/http"
)

// Game codes are the first path element of the game's browser URLs on www01.kddi-mmbb.jp.
const (
	gameCodeZGVSZ = "00000020" // SLPS-25419
	gameCodeAEUG  = "00000004" // SLPS-25305
)

type gameCodeKey struct{}

func withGameCode(code string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), gameCodeKey{}, code)))
	})
}

func gameCodeOf(r *http.Request) string {
	if code, ok := r.Context().Value(gameCodeKey{}).(string); ok {
		return code
	}
	return gameCodeZGVSZ
}

// zone keeps players apart: lobbies, rooms and battles are per game and platform.
// The games cannot battle each other.
type zone struct{ game, platform string }

func zoneOf(game, platform string) zone {
	if game == "" {
		game = gameCodeZGVSZ
	}
	return zone{game, platform}
}

func (p *LbsPeer) zone() zone   { return zoneOf(p.Game, p.Platform) }
func (l *LbsLobby) zone() zone  { return zoneOf(l.Game, l.Platform) }
func (b *LbsBattle) zone() zone { return zoneOf(b.Game, b.Platform) }

func gameTitle(code string) string {
	if code == gameCodeAEUG {
		return "エゥーゴvs.ティターンズ"
	}
	return "ガンダムvs.Zガンダム"
}

func apiGameCode(code string) string {
	if code == gameCodeAEUG {
		return code
	}
	return gameCodeZGVSZ
}
