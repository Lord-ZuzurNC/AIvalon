# Vault file encryption — état (2026-11-24, suite de nuit)

Scope demandé par Lord : chiffrement complet avant la release 0.1,
sans étape de migration séparée (installs neuves, rien à migrer —
et lui-même repart d'un dossier propre).

## Fait et vérifié

- **Primitives** : AES-256-GCM, clé dérivée côté client au unlock
  (`sha256("aivalon-vault-enc:" + salt + pin)`), jamais écrite sur
  disque. `vault_crypto.go` + `vault_crypto_test.go`.
- **Convention de chemin** : `uploads/vault/...` = chiffré, tout le
  reste en clair. Pure fonction du chemin, pas de flag en base.
- **Images de chat générées** (session vaultée) : chiffrées à
  l'écriture, servies déchiffrées via `GET /vault/image/{path...}`.
- **Avatars de personnages** (y compris la galerie d'images
  secondaires, pas juste la primaire) et **couvertures de lorebooks** :
  chiffrés/déchiffrés au moment du `MoveIn`/`MoveOut` — pas une
  migration à part, ça fait partie du geste normal de "cacher"/
  "sortir du vault". `vault_image_migrate.go`.
- **"Pas de PIN pour cacher" préservé** (ton spec d'origine) : `MoveIn`
  n'exige toujours aucune preuve du PIN. S'il se trouve que ce
  navigateur a déjà une clé en main (vault déjà déverrouillé), le
  fichier est chiffré tout de suite ; sinon il reste en clair jusqu'au
  **prochain déverrouillage réel**, qui rattrape automatiquement tout
  ce qui a été laissé en clair (`reconcileVaultImages`, appelé à
  chaque `/vault/unlock` réussi) — jamais une "migration" à lancer à
  part, juste ce qui se passe en arrière-plan la prochaine fois que tu
  tapes ton PIN.
- **`MoveOut`** redemande déjà le PIN (comportement existant) ; collecte
  maintenant aussi la clé de chiffrement au même moment, pour déchiffrer
  l'image en la sortant du vault.
- **Nettoyage de l'ancien fichier** : chaque déplacement chiffré↔clair
  supprime l'ancien fichier (pas d'orphelin plaintext qui traîne après
  un aller-retour dans le vault).
- **Tous les gabarits** passent maintenant par `imageSrc()` au lieu de
  concaténer `"/static/"` à la main — `@Avatar`, `vignetteFace`,
  `ImageField`, la galerie de personnage, la galerie de chat.
- **Tests de bout en bout réels** (`vault_image_test.go`,
  `vault_move_images_test.go`) : cycle complet chiffrement→
  déchiffrement sur un avatar de personnage, sur une image de chat
  générée, et le cas "MoveIn sans clé disponible puis rattrapé au
  prochain unlock" — les trois avec de vrais fichiers sur disque, pas
  des mocks. Traversée de chemin (`../..`) testée et rejetée sur la
  route de service.
- **Script de déchiffrement manuel** (`scripts/vault_decrypt.py`,
  Python + `cryptography`) testé pour de vrai contre un fichier chiffré
  par le code Go — contenu identique récupéré. Même procédure
  Linux/Windows.

## Ce qui reste hors scope (pas vaultable du tout, donc rien à faire)

- Les **personas** ne font pas partie de `vaultKinds` (chats/
  characters/worlds seulement) — leur avatar ne peut pas être caché
  dans le vault aujourd'hui, donc rien à chiffrer là.
- **Changer le PIN** (Setup) ne re-chiffre pas les fichiers déjà
  vaultés sous l'ancien PIN — pas construit, pas demandé ce soir.
