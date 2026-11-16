package db

import (
	"database/sql"
	"fmt"
)

// CharacterCardImage is one row in a character card's image gallery.
// CharacterCard.ImagePath (used everywhere else — Avatar, chat cover,
// etc.) is resolved via a JOIN on character_cards.primary_image_id, not
// stored redundantly here.
type CharacterCardImage struct {
	ID              int64
	CharacterCardID int64
	ImagePath       string
}

func ListCharacterCardImages(db *sql.DB, characterCardID int64) ([]CharacterCardImage, error) {
	rows, err := db.Query(
		`SELECT id, character_card_id, image_path FROM character_card_images WHERE character_card_id = ? ORDER BY id`,
		characterCardID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing images for character card %d: %w", characterCardID, err)
	}
	defer rows.Close()

	var images []CharacterCardImage
	for rows.Next() {
		var img CharacterCardImage
		if err := rows.Scan(&img.ID, &img.CharacterCardID, &img.ImagePath); err != nil {
			return nil, fmt.Errorf("scanning character card image: %w", err)
		}
		images = append(images, img)
	}
	return images, rows.Err()
}

// CreateCharacterCardImage inserts a placeholder row (imagePath "")
// before the file is actually written — the row's own id becomes part
// of the on-disk filename (see saveUploadedImage's entityID param),
// so the id has to exist first. UpdateCharacterCardImagePath fills in
// the real path once the file is saved.
func CreateCharacterCardImage(db *sql.DB, characterCardID int64, imagePath string) (int64, error) {
	res, err := db.Exec(
		`INSERT INTO character_card_images (character_card_id, image_path) VALUES (?, ?)`,
		characterCardID, imagePath,
	)
	if err != nil {
		return 0, fmt.Errorf("creating character card image: %w", err)
	}
	return res.LastInsertId()
}

func UpdateCharacterCardImagePath(db *sql.DB, imageID int64, imagePath string) error {
	_, err := db.Exec(`UPDATE character_card_images SET image_path = ? WHERE id = ?`, imagePath, imageID)
	if err != nil {
		return fmt.Errorf("updating character card image %d: %w", imageID, err)
	}
	return nil
}

// DeleteCharacterCardImage removes a gallery row. If it was the
// character's current primary, the schema's ON DELETE SET NULL on
// character_cards.primary_image_id clears that pointer automatically —
// no extra app-level "pick a new primary" step needed.
func DeleteCharacterCardImage(db *sql.DB, imageID int64) error {
	_, err := db.Exec(`DELETE FROM character_card_images WHERE id = ?`, imageID)
	if err != nil {
		return fmt.Errorf("deleting character card image %d: %w", imageID, err)
	}
	return nil
}

// SetPrimaryCharacterCardImage promotes an existing gallery image to be
// the character's avatar everywhere else — a pure pointer swap, no file
// moved or copied on disk.
func SetPrimaryCharacterCardImage(db *sql.DB, characterCardID, imageID int64) error {
	_, err := db.Exec(`UPDATE character_cards SET primary_image_id = ? WHERE id = ?`, imageID, characterCardID)
	if err != nil {
		return fmt.Errorf("setting primary image for character card %d: %w", characterCardID, err)
	}
	return nil
}

// HasPrimaryImage reports whether a character already has a primary
// image set — used to decide whether a fresh upload should auto-become
// primary (the first image on a card) or just join the gallery as a
// secondary one.
func HasPrimaryImage(db *sql.DB, characterCardID int64) (bool, error) {
	var has bool
	err := db.QueryRow(`SELECT primary_image_id IS NOT NULL FROM character_cards WHERE id = ?`, characterCardID).Scan(&has)
	if err != nil {
		return false, fmt.Errorf("checking primary image for character card %d: %w", characterCardID, err)
	}
	return has, nil
}
