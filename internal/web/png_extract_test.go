package web

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

// buildTestPNG assembles a structurally-valid (if not decodable as an
// actual image) PNG: signature + IHDR + one text-ish chunk per
// extraChunks + IEND. Good enough for pngTextChunks/pngCharaJSON,
// which only ever look at chunk type/length/data, never the pixels.
func buildTestPNG(t *testing.T, extraChunks ...[2]string) []byte {
	t.Helper()
	chunk := func(ctype string, data []byte) []byte {
		var buf bytes.Buffer
		length := make([]byte, 4)
		binary.BigEndian.PutUint32(length, uint32(len(data)))
		buf.Write(length)
		buf.WriteString(ctype)
		buf.Write(data)
		crc := crc32.ChecksumIEEE(append([]byte(ctype), data...))
		crcBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(crcBytes, crc)
		buf.Write(crcBytes)
		return buf.Bytes()
	}
	var out bytes.Buffer
	out.Write(pngSignature)
	out.Write(chunk("IHDR", make([]byte, 13)))
	for _, kv := range extraChunks {
		out.Write(chunk(kv[0], []byte(kv[1])))
	}
	out.Write(chunk("IEND", nil))
	return out.Bytes()
}

func tEXtChunk(keyword, text string) [2]string {
	return [2]string{"tEXt", keyword + "\x00" + text}
}

func zTXtChunk(t *testing.T, keyword, text string) [2]string {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	return [2]string{"zTXt", keyword + "\x00\x00" + buf.String()}
}

func TestPNGCharaJSON_TEXtChunk(t *testing.T) {
	cardJSON := `{"spec":"chara_card_v2","data":{"name":"PNG Hero"}}`
	b64 := base64.StdEncoding.EncodeToString([]byte(cardJSON))
	png := buildTestPNG(t, tEXtChunk("chara", b64))
	decoded, ok := pngCharaJSON(png)
	if !ok {
		t.Fatal("expected a tEXt \"chara\" chunk to be found")
	}
	if string(decoded) != cardJSON {
		t.Fatalf("got %q, want %q", decoded, cardJSON)
	}
}

func TestPNGCharaJSON_ZTXtChunk(t *testing.T) {
	cardJSON := `{"spec":"chara_card_v2","data":{"name":"Compressed Hero"}}`
	b64 := base64.StdEncoding.EncodeToString([]byte(cardJSON))
	png := buildTestPNG(t, zTXtChunk(t, "chara", b64))
	decoded, ok := pngCharaJSON(png)
	if !ok {
		t.Fatal("expected a zTXt \"chara\" chunk to be decompressed and found")
	}
	if string(decoded) != cardJSON {
		t.Fatalf("got %q, want %q", decoded, cardJSON)
	}
}

func TestPNGCharaJSON_PrefersCCv3(t *testing.T) {
	v2JSON := `{"spec":"chara_card_v2","data":{"name":"V2"}}`
	v3JSON := `{"spec":"chara_card_v3","data":{"name":"V3"}}`
	png := buildTestPNG(t,
		tEXtChunk("chara", base64.StdEncoding.EncodeToString([]byte(v2JSON))),
		tEXtChunk("ccv3", base64.StdEncoding.EncodeToString([]byte(v3JSON))),
	)
	decoded, ok := pngCharaJSON(png)
	if !ok || string(decoded) != v3JSON {
		t.Fatalf("expected ccv3 to win over chara, got ok=%v decoded=%q", ok, decoded)
	}
}

func TestPNGCharaJSON_NoCharaChunk(t *testing.T) {
	png := buildTestPNG(t, tEXtChunk("Comment", "just a regular PNG comment"))
	if _, ok := pngCharaJSON(png); ok {
		t.Fatal("expected a PNG with no chara/ccv3 chunk to report not-found")
	}
}

func TestExtractJSONFromUpload_PlainJSONPassesThrough(t *testing.T) {
	raw := []byte(`{"hello":"world"}`)
	out, err := extractJSONFromUpload(raw)
	if err != nil || string(out) != string(raw) {
		t.Fatalf("expected non-PNG input to pass through unchanged, got out=%q err=%v", out, err)
	}
}

func TestExtractJSONFromUpload_PNGWithoutChara(t *testing.T) {
	png := buildTestPNG(t, tEXtChunk("Comment", "no card here"))
	if _, err := extractJSONFromUpload(png); err == nil {
		t.Fatal("expected a PNG with no embedded card to error, not silently import nothing")
	}
}
