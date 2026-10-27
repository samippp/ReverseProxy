// Package secret serves the legacy backend's protected message.
package secret

import (
	"log"
	"net/http"
)

// Handler replies with a secret message naming X-User-Id.
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-User-Id")
		_, err := w.Write([]byte("This is a secret message. If you get this, you have been authorized. Your userID is " + userID))
		if err != nil {
			log.Println("Unable to Write body")
		}
	}
}
