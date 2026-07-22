package server

import (
	"net/http"

	"presync/internal/config"
)

func withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Presync-Token")
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing authentication token"})
			return
		}

		if _, ok := config.ValidateToken(token); ok {
			next(w, r)
			return
		}

		if localToken, err := config.GetLocalToken(); err == nil && token == localToken {
			next(w, r)
			return
		}

		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or revoked token"})
	}
}
