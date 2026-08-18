package secret

import (
	"log"
	"net/http"
)

func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-User-Id")
		_, err := w.Write([]byte("This is a secret message. If you get this, you have been authorized. Your userID is " + userID))
		if err != nil {
			log.Println("Unable to Write body")
		}
	}
}
