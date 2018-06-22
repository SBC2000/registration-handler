package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

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
			log.WithField("method", r.Method).Error("Invalid method")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		if r.Header.Get("X-hook-secret") != os.Getenv("WEBHOOK_SECRET") {
			log.WithField("secret", r.Header.Get("X-hook-secret")).Error("Invalid secret")
			http.Error(w, "Invalid Secret", http.StatusForbidden)
			return
		}

		var (
			buffer []byte
			err    error
		)

		defer r.Body.Close()
		if buffer, err = ioutil.ReadAll(r.Body); err != nil {
			log.WithField("error", err).Error("Cannot read body")
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		log.WithField("body", string(buffer)).Info("Request body read")

		var msg form.Message
		if err = json.Unmarshal(buffer, &msg); err != nil {
			log.WithField("error", err).Error("Cannot parse body")
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if r.Header.Get("X-test") != "" {
			log.Info("Received test message")

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
			log.WithField("message", msg).Info("Received message")

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

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		log.WithField("method", r.Method).Info("/health")

		if _, err := w.Write([]byte("OK")); err != nil {
			log.WithField("error", err).Error("Failed to handle health request")
			http.Error(w, "Could not return health OK", http.StatusInternalServerError)
		}
	})

	ticker := time.NewTicker(10 * time.Minute)
	go func() {
		baseURL := os.Getenv("BASE_URL")
		for range ticker.C {
			http.Get(fmt.Sprintf("%s/%s", baseURL, "health"))
		}
	}()

	http.ListenAndServe(fmt.Sprintf(":%s", os.Getenv("PORT")), nil)
}
