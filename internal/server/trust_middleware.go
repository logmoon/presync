package server

import (
	"net"
	"net/http"
	"strings"

	"presync/internal/config"
)

// withAuth validates a device token from devices.json. The local token is
// additionally accepted, but ONLY over loopback connections (the CLI's
// self-sync on the server machine). A local token obtained remotely via the
// /web/token bootstrap can never reach file download or sync mutation routes.
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

		if localToken, err := config.GetLocalToken(); err == nil && token == localToken && isLoopback(r.RemoteAddr) {
			next(w, r)
			return
		}

		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or revoked token"})
	}
}

// withWebAuth accepts either a paired-device token or the local token. Used
// for web-UI surface routes (status, profiles, web sync trigger) that the
// desktop browser reaches via the local token bootstrap.
func withWebAuth(next http.HandlerFunc) http.HandlerFunc {
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

// withLocalAdmin requires the local token AND a loopback connection. Used for
// admin actions that only the CLI on the server machine performs (pairing
// confirm/discard, reload). The loopback check closes the pairing-hijack
// vector: an attacker on the LAN cannot reach 127.0.0.1.
func withLocalAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin actions are only available on the server machine"})
			return
		}

		token := r.Header.Get("X-Presync-Token")
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing authentication token"})
			return
		}

		if localToken, err := config.GetLocalToken(); err == nil && token == localToken {
			next(w, r)
			return
		}

		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid local token"})
	}
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
