package form

import (
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// Message describes a form submission from wordpress
type Message struct {
	Title string            `json:"title"`
	Data  map[string]string `json:"data"`
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
	case "Sign up teams":
		lang = en
	default:
		log.WithField("title", message.Title).Info("Ignoring message")
		return
	}

	var form form
	if form, err = parseData(message.Data, lang); err != nil {
		return
	}

	err = h.storeForm(form, lang)

	return
}

func (h *handler) storeForm(form form, language language) (err error) {
	var tx *sql.Tx
	if tx, err = h.db.Begin(); err != nil {
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

	lastInsertID := 0
	tx.QueryRow(`
		INSERT INTO inschrijving (
			inschrijfnummer, jaar, voornaam, achternaam, email, telefoon, vereniging, taal, inschrijfdatum
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id
		`,
		subscriptionID,
		year,
		form.Name,
		form.Surname,
		form.Email,
		form.Phone,
		form.Club,
		string(language),
		form.SubmitTime,
	).Scan(&lastInsertID)

	placeholders := make([]string, 0, len(form.Teams))
	values := make([]interface{}, 0, 4*len(form.Teams))

	for _, team := range form.Teams {
		placeholders = append(placeholders, "(?, ?, ?, ?)")
		values = append(values, lastInsertID, team.Name, team.Type, team.Level)
	}

	if _, err = tx.Query(`
		INSERT INTO team (inschrijvingsid, teamnaam, type, niveau)
		VALUES
	`+strings.Join(placeholders, ","),
		values...); err != nil {
		return
	}

	err = tx.Commit()

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

	if err == nil {
		submitTimeParts := strings.Split(readEntry("submit-time"), ".")
		if len(submitTimeParts) == 0 {
			err = errors.New("Unknown submit time format")
		}
		var submitTimeUnix int64
		if submitTimeUnix, err = strconv.ParseInt(submitTimeParts[0], 10, 64); err != nil {
			return
		}
		parsed.SubmitTime = time.Unix(submitTimeUnix, 0)
	}

	for i := 0; i < 5; i++ {
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
