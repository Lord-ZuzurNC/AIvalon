package web

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

type CharacterHandlers struct {
	DB *sql.DB
}

func (h CharacterHandlers) List(w http.ResponseWriter, r *http.Request) {
	cards, err := db.ListCharacterCards(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, CharacterPage(cards, nil))
}

func (h CharacterHandlers) EditForm(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	card, err := db.GetCharacterCard(h.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	cards, err := db.ListCharacterCards(h.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, r, CharacterPage(cards, &card))
}

func (h CharacterHandlers) Create(w http.ResponseWriter, r *http.Request) {
	c := characterCardFromForm(r, db.CharacterCard{})
	if _, err := db.CreateCharacterCard(h.DB, c); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/characters", http.StatusSeeOther)
}

func (h CharacterHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	c := characterCardFromForm(r, db.CharacterCard{ID: id})
	if err := db.UpdateCharacterCard(h.DB, c); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/characters", http.StatusSeeOther)
}

func (h CharacterHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := db.DeleteCharacterCard(h.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/characters", http.StatusSeeOther)
}

func characterCardFromForm(r *http.Request, base db.CharacterCard) db.CharacterCard {
	r.ParseForm()
	base.Name = r.FormValue("name")
	base.Description = r.FormValue("description")
	base.Personality = r.FormValue("personality")
	base.Scenario = r.FormValue("scenario")
	base.FirstMessage = r.FormValue("first_message")
	base.ExampleMessages = r.FormValue("example_messages")
	return base
}
