package web

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// extractJSONFromUpload passes plain JSON uploads through unchanged;
// for a PNG it pulls out the embedded Character Card JSON (the
// "chara"/"ccv3" text chunk SillyTavern-style tools write as base64)
// so the rest of the import pipeline — parseLorebookJSON,
// parseCharacterJSON, the cross-import confirmation — never has to
// know whether the upload was a .json or a .png in the first place.
func extractJSONFromUpload(raw []byte) ([]byte, error) {
	if !bytes.HasPrefix(raw, pngSignature) {
		return raw, nil
	}
	decoded, ok := pngCharaJSON(raw)
	if !ok {
		return nil, fmt.Errorf(`this PNG has no embedded Character Card metadata (no "chara" or "ccv3" text chunk) — it needs to come from a tool that embeds the card, like SillyTavern's own export`)
	}
	return decoded, nil
}

// pngCharaJSON walks a PNG's text chunks for the embedded card JSON.
// "ccv3" is preferred over "chara" when both are present — it's
// meant to carry the fuller V3 card when the two differ, though in
// practice most exporters just write the same payload to both.
func pngCharaJSON(raw []byte) ([]byte, bool) {
	chunks := pngTextChunks(raw)
	for _, keyword := range []string{"ccv3", "chara"} {
		b64, ok := chunks[keyword]
		if !ok {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err == nil && json.Valid(decoded) {
			return decoded, true
		}
	}
	return nil, false
}

// pngTextChunks returns every tEXt/zTXt/iTXt chunk's text keyed by
// its keyword, decompressing zTXt/compressed-iTXt as needed. A
// malformed or truncated chunk stream just stops early and returns
// whatever was already found, rather than erroring the whole import
// over metadata this app doesn't otherwise care about.
func pngTextChunks(raw []byte) map[string]string {
	out := make(map[string]string)
	if len(raw) < 8 || !bytes.Equal(raw[:8], pngSignature) {
		return out
	}
	pos := 8
	for pos+8 <= len(raw) {
		length := binary.BigEndian.Uint32(raw[pos : pos+4])
		ctype := string(raw[pos+4 : pos+8])
		dataStart := pos + 8
		dataEnd := dataStart + int(length)
		if dataEnd < dataStart || dataEnd+4 > len(raw) {
			break
		}
		data := raw[dataStart:dataEnd]
		switch ctype {
		case "tEXt":
			if kw, txt, ok := splitNulByte(data); ok {
				out[kw] = string(txt)
			}
		case "zTXt":
			if kw, rest, ok := splitNulByte(data); ok && len(rest) > 1 {
				if plain, err := zlibDecompress(rest[1:]); err == nil {
					out[kw] = string(plain)
				}
			}
		case "iTXt":
			if kw, rest, ok := splitNulByte(data); ok && len(rest) >= 2 {
				compFlag := rest[0]
				afterMethod := rest[2:]
				if _, afterLang, ok := splitNulByte(afterMethod); ok {
					if _, text, ok := splitNulByte(afterLang); ok {
						if compFlag == 1 {
							if plain, err := zlibDecompress(text); err == nil {
								out[kw] = string(plain)
							}
						} else {
							out[kw] = string(text)
						}
					}
				}
			}
		case "IEND":
			return out
		}
		pos = dataEnd + 4 // skip the chunk's CRC
	}
	return out
}

func splitNulByte(b []byte) (before string, after []byte, ok bool) {
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return "", nil, false
	}
	return string(b[:i]), b[i+1:], true
}

func zlibDecompress(b []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
