package form

import (
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// Message describes a form submission from wordpress
type Message struct {
	Title string            `json:"title"`
	Data  map[string]string `json:"posted_data"`
}

type form struct {
	Club       string
	Name       string
	Surname    string
	Email      string
	Phone      string
	SubmitTime time.Time
	Teams      []team
}

type team struct {
	Name  string
	Type  string
	Level string
}

type language string

const (
	nl = language("NL")
	en = language("EN")
)

// Handler handles form submissions
type Handler interface {
	Handle(message Message) error
}

type handler struct {
	subscriptionIDs map[string]struct{}
	db              *sql.DB
	rng             *rand.Rand
}

// NewHandler creates a new Handler
func NewHandler(db *sql.DB) (h Handler, err error) {
	subscriptionIDs := make(map[string]struct{})
	var rows *sql.Rows
	if rows, err = db.Query("SELECT inschrijfnummer FROM inschrijving"); err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var subscriptionID string
		if err = rows.Scan(&subscriptionID); err != nil {
			return
		}
		subscriptionIDs[subscriptionID] = struct{}{}
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	h = &handler{
		subscriptionIDs,
		db,
		rng,
	}

	return
}

func (h *handler) Handle(message Message) (err error) {
	var lang language

	switch message.Title {
	case "Inschrijven teams":
		lang = nl
		log.Info("Handling Dutch form")
	case "Sign up teams":
		lang = en
		log.Info("Handling English form")
	default:
		log.WithField("title", message.Title).Info("Ignoring message")
		return
	}

	var form form
	if form, err = parseData(message.Data, lang); err != nil {
		log.WithFields(log.Fields(map[string]interface{}{
			"error": err,
			"data":  message.Data,
		})).Error("Failed to parse data")
		return
	}

	if err = h.storeForm(form, lang); err != nil {
		log.WithField("error", err).Error("Failed to store form")
	}

	return
}

func (h *handler) storeForm(form form, language language) (err error) {
	var tx *sql.Tx
	if tx, err = h.db.Begin(); err != nil {
		log.WithField("error", err).Error("Failed to start transaction")
		return
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	subscriptionID := h.createSubscriptionID()

	// this is not how it used to work but since the sign-up season typically runs from
	// April to August, this should be safe enough
	year := time.Now().Year()

	query := `
		INSERT INTO inschrijving (
			inschrijfnummer, jaar, voornaam, achternaam, email, telefoon, vereniging, taal, inschrijfdatum
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`

	log.WithFields(log.Fields(map[string]interface{}{
		"query":          query,
		"subscriptionID": subscriptionID,
		"year":           year,
		"name":           form.Name,
		"surname":        form.Surname,
		"email":          form.Email,
		"phone":          form.Phone,
		"club":           form.Club,
		"language":       string(language),
		"submitTime":     form.SubmitTime,
	})).Info("Insert inschrijving")

	if _, err = tx.Exec(query,
		subscriptionID,
		year,
		form.Name,
		form.Surname,
		form.Email,
		form.Phone,
		form.Club,
		string(language),
		form.SubmitTime,
	); err != nil {
		log.WithField("error", err).Error("Failed to create subscription")
		return
	}

	placeholders := make([]string, 0, len(form.Teams))
	values := make([]interface{}, 0, 3*len(form.Teams))

	for i, team := range form.Teams {
		placeholders = append(
			placeholders,
			fmt.Sprintf("(currval('inschrijving_id_seq'), $%d, $%d, $%d)", 3*i+1, 3*i+2, 3*i+3),
		)
		values = append(values, team.Name, team.Type, team.Level)
	}

	query = `
		INSERT INTO team (inschrijvingsid, teamnaam, "type", niveau)
		VALUES
	` + strings.Join(placeholders, ",")

	log.WithFields(log.Fields(map[string]interface{}{
		"query":  query,
		"values": values,
	})).Info("Inserting teams")

	if _, err = tx.Exec(query, values...); err != nil {
		log.WithField("error", err).Error("Failed to create teams")
		return
	}

	if err = tx.Commit(); err != nil {
		log.WithField("error", err).Error("Failed to commit transaction")
	}

	return
}

// Note: not thread-safe but should be good enough in practice
func (h *handler) createSubscriptionID() string {
	for {
		newID := fmt.Sprintf("%06d", h.rng.Int()%1000000)
		if _, exists := h.subscriptionIDs[newID]; !exists {
			h.subscriptionIDs[newID] = struct{}{}
			return newID
		}
	}
}

func parseData(data map[string]string, language language) (parsed form, err error) {
	readEntry := func(key string) (value string) {
		if err == nil {
			if value = data[key]; value == "" {
				err = fmt.Errorf("Missing required value: %s", key)
			}
		}
		return
	}

	parsed.Club = readEntry("contact-club")
	parsed.Name = readEntry("contact-name")
	parsed.Surname = readEntry("contact-surname")
	parsed.Email = readEntry("contact-email")
	parsed.Phone = readEntry("contact-phone")
	parsed.SubmitTime = time.Now()

	for i := 1; i <= 5; i++ {
		if parsedTeam := parseTeam(data, language, i); parsedTeam != nil {
			parsed.Teams = append(parsed.Teams, *parsedTeam)
		}
	}

	if err == nil && len(parsed.Teams) == 0 {
		err = errors.New("Subscription contains no teams")
	}

	return
}

func parseTeam(data map[string]string, language language, index int) (parsed *team) {
	if name := data[fmt.Sprintf("team%d-name", index)]; name != "" {
		parsed = &team{
			Name:  name,
			Type:  data[fmt.Sprintf("team%d-type", index)],
			Level: data[fmt.Sprintf("team%d-level", index)],
		}

		// convert English terms to Dutch equivalents
		if language == en {
			switch parsed.Type {
			case "Men":
				parsed.Type = "Heren"
			case "Women":
				parsed.Type = "Dames"
			default:
				parsed.Type = "Onbekend, check registration-handler"
			}

			switch parsed.Level {
			case "National":
				parsed.Level = "Bond 2"
			case "Regional High":
				parsed.Level = "Regio 1"
			case "Regional Low":
				parsed.Level = "Regio 3-4"
			default:
				parsed.Level = "Onbekend, check registration-handler"
			}
		}
	}

	return
}
