package web

// vault_image_migrate.go: moving an entity's existing image(s) between
// plaintext and vault-encrypted storage when its vaulted status
// changes — called from MoveIn/MoveOut (handlers_vault.go) when a key
// is available, and from reconcileVaultImages (also here) which fixes
// up anything that wasn't (MoveIn needs no PIN per Lord's own spec, so
// there's no guaranteed key at that exact moment — see VaultGate.EncKey).

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Lord-ZuzurNC/AIvalon/internal/db"
)

// moveImageFile moves one stored image between plaintext and
// encrypted-at-rest storage (the uploads/vault/ convention —
// vault_crypto.go), returning the path to store in the DB. A no-op
// (returns oldPath unchanged) when oldPath is already in the target
// state or empty, so callers can call this unconditionally.
func moveImageFile(key []byte, oldPath, subdir string, entityID int64, toVaulted bool) (string, error) {
	if oldPath == "" {
		return oldPath, nil
	}
	currentlyVaulted := strings.HasPrefix(stripQuery(oldPath), "uploads/vault/")
	if currentlyVaulted == toVaulted {
		return oldPath, nil
	}
	diskOld := filepath.Join("web", "static", stripQuery(oldPath))
	data, err := os.ReadFile(diskOld)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", diskOld, err)
	}
	var newPath string
	if toVaulted {
		newPath, err = saveVaultImageBytes(key, data, subdir, entityID)
	} else {
		var plaintext []byte
		if plaintext, err = decryptVaultBytes(key, data); err == nil {
			newPath, err = saveImageBytes(plaintext, subdir, entityID)
		}
	}
	if err != nil {
		return "", err
	}
	// currentlyVaulted != toVaulted was just established, so old and
	// new always live in different directories — no risk of deleting
	// the file just written (see handlers_world.go's saveImage for
	// the case where that risk is real).
	removeUploadedFile(oldPath)
	return newPath, nil
}

// moveEntityImages dispatches by vault kind to the right rows: a chat
// session can have many generated-image messages, a character can
// have many gallery images (incl. its primary), a lorebook has
// exactly one cover. Every failure is collected and returned joined,
// not short-circuited — one bad file shouldn't stop the others from
// moving.
func moveEntityImages(sqlDB *sql.DB, key []byte, kindKey string, id int64, toVaulted bool) error {
	switch kindKey {
	case "chats":
		return moveSessionImages(sqlDB, key, id, toVaulted)
	case "characters":
		return moveCharacterImages(sqlDB, key, id, toVaulted)
	case "worlds":
		return moveLorebookImage(sqlDB, key, id, toVaulted)
	default:
		return nil
	}
}

func moveSessionImages(sqlDB *sql.DB, key []byte, sessionID int64, toVaulted bool) error {
	messages, err := db.ListMessages(sqlDB, sessionID)
	if err != nil {
		return err
	}
	var errs []string
	for _, m := range messages {
		if m.Role != "image" || m.ImagePath == "" {
			continue
		}
		newPath, err := moveImageFile(key, m.ImagePath, "generated", m.ID, toVaulted)
		if err != nil {
			errs = append(errs, fmt.Sprintf("message %d: %v", m.ID, err))
			continue
		}
		if newPath != m.ImagePath {
			if err := db.SetMessageImagePath(sqlDB, m.ID, newPath); err != nil {
				errs = append(errs, fmt.Sprintf("message %d: saving new path: %v", m.ID, err))
			}
		}
	}
	return joinErrs(errs)
}

func moveCharacterImages(sqlDB *sql.DB, key []byte, characterID int64, toVaulted bool) error {
	images, err := db.ListCharacterCardImages(sqlDB, characterID)
	if err != nil {
		return err
	}
	var errs []string
	for _, img := range images {
		if img.ImagePath == "" {
			continue
		}
		newPath, err := moveImageFile(key, img.ImagePath, "characters", img.ID, toVaulted)
		if err != nil {
			errs = append(errs, fmt.Sprintf("image %d: %v", img.ID, err))
			continue
		}
		if newPath != img.ImagePath {
			if err := db.UpdateCharacterCardImagePath(sqlDB, img.ID, newPath); err != nil {
				errs = append(errs, fmt.Sprintf("image %d: saving new path: %v", img.ID, err))
			}
		}
	}
	return joinErrs(errs)
}

func moveLorebookImage(sqlDB *sql.DB, key []byte, lorebookID int64, toVaulted bool) error {
	book, err := db.GetLorebook(sqlDB, lorebookID)
	if err != nil {
		return err
	}
	if book.ImagePath == "" {
		return nil
	}
	newPath, err := moveImageFile(key, book.ImagePath, "lorebooks", lorebookID, toVaulted)
	if err != nil {
		return err
	}
	if newPath != book.ImagePath {
		return db.UpdateLorebookImagePath(sqlDB, lorebookID, newPath)
	}
	return nil
}

// reconcileVaultImages runs once per successful real /vault/unlock
// (handlers_vault.go — not Setup's PIN-change path, which has no key
// to do this with): every currently-vaulted session/character/
// lorebook gets its image(s) checked, and anything still sitting in
// plaintext gets encrypted+moved now. This is what makes MoveIn's
// "no PIN needed to hide" promise safe to keep — whatever a MoveIn
// couldn't encrypt on the spot (no key available at that moment)
// never stays exposed longer than until the next real unlock.
func reconcileVaultImages(sqlDB *sql.DB, key []byte) error {
	var errs []string

	sessions, err := db.ListVaultedSessions(sqlDB)
	if err != nil {
		errs = append(errs, fmt.Sprintf("listing vaulted sessions: %v", err))
	}
	for _, s := range sessions {
		if err := moveSessionImages(sqlDB, key, s.ID, true); err != nil {
			errs = append(errs, fmt.Sprintf("session %d: %v", s.ID, err))
		}
	}

	characters, err := db.ListVaultedCharacterCards(sqlDB)
	if err != nil {
		errs = append(errs, fmt.Sprintf("listing vaulted characters: %v", err))
	}
	for _, c := range characters {
		if err := moveCharacterImages(sqlDB, key, c.ID, true); err != nil {
			errs = append(errs, fmt.Sprintf("character %d: %v", c.ID, err))
		}
	}

	books, err := db.ListVaultedLorebooks(sqlDB)
	if err != nil {
		errs = append(errs, fmt.Sprintf("listing vaulted lorebooks: %v", err))
	}
	for _, b := range books {
		if err := moveLorebookImage(sqlDB, key, b.ID, true); err != nil {
			errs = append(errs, fmt.Sprintf("lorebook %d: %v", b.ID, err))
		}
	}

	return joinErrs(errs)
}

func joinErrs(errs []string) error {
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(errs, "; "))
}
