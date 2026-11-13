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
