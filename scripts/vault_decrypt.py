#!/usr/bin/env python3
"""vault_decrypt.py — standalone decryptor for AIvalon's vault-encrypted
files (web/static/uploads/vault/...). No AIvalon code needed, no
server needed to be running: just this script, your PIN, and the
aivalon.db file (to read the non-secret salt).

Matches internal/web/vault_crypto.go exactly:
  - key = sha256("aivalon-vault-enc:" + salt + pin)   (32 bytes, used directly as the AES-256 key)
  - file = 12-byte random nonce || AES-256-GCM(key, nonce, plaintext)

Usage
-----
Decrypt one file:
    python3 vault_decrypt.py --db aivalon.db --in path/to/5.png.enc --out 5.png

Decrypt everything under web/static/uploads/vault/ into a folder:
    python3 vault_decrypt.py --db aivalon.db --all web/static/uploads/vault --out-dir decrypted/

The PIN is asked for interactively (hidden input) — never pass it as
a command-line argument, that leaves it sitting in your shell history.

Setup (one-time)
-----------------
Linux:
    python3 -m pip install --user cryptography

Windows (PowerShell or cmd):
    py -m pip install --user cryptography
"""
import argparse
import getpass
import hashlib
import os
import sqlite3
import sys
from pathlib import Path

try:
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM
except ImportError:
    sys.exit(
        "Missing dependency 'cryptography'. Install it with:\n"
        "  Linux:   python3 -m pip install --user cryptography\n"
        "  Windows: py -m pip install --user cryptography"
    )

NONCE_SIZE = 12
DOMAIN_PREFIX = "aivalon-vault-enc:"


def read_salt(db_path: str) -> str:
    con = sqlite3.connect(db_path)
    try:
        row = con.execute("SELECT pin_salt FROM vault WHERE id = 1").fetchone()
    finally:
        con.close()
    if not row:
        sys.exit(f"No vault configured in {db_path} (no row in the 'vault' table).")
    return row[0]


def derive_key(salt: str, pin: str) -> bytes:
    return hashlib.sha256((DOMAIN_PREFIX + salt + pin).encode("utf-8")).digest()


def decrypt_bytes(key: bytes, data: bytes) -> bytes:
    if len(data) < NONCE_SIZE:
        raise ValueError("file too short to contain a nonce")
    nonce, ciphertext = data[:NONCE_SIZE], data[NONCE_SIZE:]
    return AESGCM(key).decrypt(nonce, ciphertext, None)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--db", required=True, help="path to aivalon.db (read-only, just for the salt)")
    group = p.add_mutually_exclusive_group(required=True)
    group.add_argument("--in", dest="infile", help="one .enc file to decrypt")
    group.add_argument("--all", dest="all_dir", help="decrypt every .enc file under this directory")
    p.add_argument("--out", help="output file path (with --in)")
    p.add_argument("--out-dir", help="output directory (with --all)")
    args = p.parse_args()

    if args.infile and not args.out:
        sys.exit("--in requires --out")
    if args.all_dir and not args.out_dir:
        sys.exit("--all requires --out-dir")

    salt = read_salt(args.db)
    pin = getpass.getpass("Vault PIN: ")
    key = derive_key(salt, pin)

    if args.infile:
        data = Path(args.infile).read_bytes()
        try:
            plaintext = decrypt_bytes(key, data)
        except Exception as e:
            sys.exit(f"Decryption failed (wrong PIN, or not a vault file?): {e}")
        Path(args.out).write_bytes(plaintext)
        print(f"Wrote {args.out} ({len(plaintext)} bytes)")
        return

    src_root = Path(args.all_dir)
    out_root = Path(args.out_dir)
    count, failed = 0, 0
    for enc_path in src_root.rglob("*.enc"):
        rel = enc_path.relative_to(src_root)
        dest = out_root / rel.with_suffix("")  # strip the trailing .enc
        dest.parent.mkdir(parents=True, exist_ok=True)
        try:
            plaintext = decrypt_bytes(key, enc_path.read_bytes())
        except Exception as e:
            print(f"  SKIP {rel}: {e}")
            failed += 1
            continue
        dest.write_bytes(plaintext)
        print(f"  OK   {rel} -> {dest}")
        count += 1
    print(f"\nDecrypted {count} file(s), {failed} failed.")


if __name__ == "__main__":
    main()
