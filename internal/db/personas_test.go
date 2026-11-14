package db

import (
	"path/filepath"
	"testing"
)

func TestPersonaCRUD(t *testing.T) {
	sqlDB, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	id, err := CreatePersona(sqlDB, Persona{Name: "Lord", Description: "A curious engineer."})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := GetPersona(sqlDB, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Lord" {
		t.Fatalf("Get returned %+v", got)
	}

	got.Description = "A curious engineer and wizard."
	if err := UpdatePersona(sqlDB, got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	personas, err := ListPersonas(sqlDB)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(personas) != 1 || personas[0].Description != "A curious engineer and wizard." {
		t.Fatalf("List after update returned %+v", personas)
	}

	if err := DeletePersona(sqlDB, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	personas, _ = ListPersonas(sqlDB)
	if len(personas) != 0 {
		t.Fatalf("expected no personas after delete, got %+v", personas)
	}
}
