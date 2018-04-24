package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	log "github.com/sirupsen/logrus"

	"github.com/SBC2000/registration-handler/form"
	_ "github.com/lib/pq"
)

type testResponse struct {
	Message string            `json:"message"`
	Data    map[string]string `json:"data"`
}

func main() {
	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.WithField("error", err).Fatal("Could not connect to database")
		return
	}

	formHandler, err := form.NewHandler(db)
	if err != nil {
		log.WithField("error", err).Fatal("Could not create formHandler")
		return
	}

	http.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		if r.Header.Get("X-hook-secret") != os.Getenv("WEBHOOK_SECRET") {
			http.Error(w, "Invalid Secret", http.StatusForbidden)
			return
		}

		var (
			buffer []byte
			err    error
		)

		defer r.Body.Close()
		if buffer, err = ioutil.ReadAll(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var msg form.Message
		if err = json.Unmarshal(buffer, &msg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if r.Header.Get("X-test") != "" {
			resp := testResponse{
				Message: "Received submission for form " + msg.Title,
				Data:    msg.Data,
			}

			if buffer, err = json.Marshal(resp); err != nil {
				log.WithField("error", err).Error("Failed to handle test message")
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			log.Info("Successfully handled test message")

			w.Header().Set("content-type", "application/json")
			w.Write(buffer)
			return
		} else {
			if err := formHandler.Handle(msg); err == nil {
				log.WithField("title", msg.Title).Info("Successfully handled message")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("OK"))
			} else {
				log.WithField("error", err).Error("Failed to handle message")
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}
	})

	http.ListenAndServe(fmt.Sprintf(":%s", os.Getenv("PORT")), nil)
}
