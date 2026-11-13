// Package packtest builds the smallest valid theme pack, for tests, as
// plain JSON values a test can break any part of.
package packtest

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"
)

// Doc is a glTF document as plain JSON values. Its "_bin" entry is the
// binary chunk GLB writes after the JSON.
type Doc map[string]any

// NewDoc is the smallest glTF a pack can use: one triangle, drawn as every
// model Manifest names, with the parts each needs.
func NewDoc() Doc {
	tri := make([]byte, 36)
	for i, f := range []float32{0, 0, 0, 1, 0, 0, 0, 1, 0} {
		binary.LittleEndian.PutUint32(tri[i*4:], math.Float32bits(f))
	}
	node := func(name string, children ...int) map[string]any {
		n := map[string]any{"name": name, "mesh": 0}
		if len(children) > 0 {
			n["children"] = children
		}
		return n
	}
	nodes := []any{
		node("citadel", 1, 2, 3), // 0
		node("core"),             // 1
		node("halo"),             // 2
		node("beacon"),           // 3
		node("island", 5),        // 4
		node("core.001"),         // 5, a second core as Blender names it
		node("ring"),             // 6
		node("plaza"),            // 7
		node("post"),             // 8
		node("building", 10),     // 9
		node("cap"),              // 10
		node("crane"),            // 11
	}
	return Doc{
		"asset":       map[string]any{"version": "2.0"},
		"scene":       0,
		"scenes":      []any{map[string]any{"nodes": []any{0, 4, 6, 7, 8, 9, 11}}},
		"nodes":       nodes,
		"meshes":      []any{map[string]any{"primitives": []any{map[string]any{"attributes": map[string]any{"POSITION": 0}, "material": 0}}}},
		"materials":   []any{map[string]any{"name": "paper"}},
		"accessors":   []any{map[string]any{"bufferView": 0, "componentType": 5126, "count": 3, "type": "VEC3", "min": []any{0, 0, 0}, "max": []any{1, 1, 0}}},
		"bufferViews": []any{map[string]any{"buffer": 0, "byteLength": 36}},
		"buffers":     []any{map[string]any{"byteLength": 36}},
		"_bin":        tri,
	}
}

// GLB writes a document as a GLB file.
func (d Doc) GLB(t testing.TB) []byte {
	t.Helper()
	doc := map[string]any{}
	for k, v := range d {
		if k != "_bin" {
			doc[k] = v
		}
	}
	bin, _ := d["_bin"].([]byte)
	js, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	pad := func(b []byte, with byte) []byte {
		b = append([]byte(nil), b...)
		for len(b)%4 != 0 {
			b = append(b, with)
		}
		return b
	}
	js = pad(js, ' ')
	bin = pad(bin, 0)
	var out bytes.Buffer
	w := func(v uint32) { _ = binary.Write(&out, binary.LittleEndian, v) }
	w(0x46546C67) // glTF
	w(2)
	w(uint32(12 + 8 + len(js) + 8 + len(bin)))
	w(uint32(len(js)))
	w(0x4E4F534A) // JSON
	out.Write(js)
	w(uint32(len(bin)))
	w(0x004E4942) // BIN
	out.Write(bin)
	return out.Bytes()
}

// Manifest is a theme.json for NewDoc's models.
const Manifest = `{
  "format": 1,
  "id": "test-pack",
  "name": "Test Pack",
  "version": "1.2.3",
  "license": "CC0-1.0",
  "sky": null,
  "buildings": {
    "rest": { "color": "#eeeeee" },
    "read": { "color": "#ddddff", "emissive": "#3355aa", "emissiveIntensity": 0.5 },
    "edit": { "color": "#ffeecc", "emissive": "#cc8822", "emissiveIntensity": 0.7 },
    "conflict": { "color": "#ffcccc", "emissive": "#aa3322", "emissiveIntensity": 0.7 },
    "selected": { "color": "#eeffff", "emissive": "#33ccdd", "emissiveIntensity": 0.5 }
  },
  "citadel": { "model": "citadel", "labelY": 12, "ringRadius": 10, "beacon": "#ffd27a" },
  "island": { "models": ["island"], "scenery": [{ "model": "ring", "perRadius": 1, "pad": 0.5, "scale": [1, 1] }] },
  "ring": { "model": "ring", "spacing": 1, "spin": 0.3 },
  "terrace": { "plaza": "plaza", "post": "post", "postSpacing": 1, "postOut": 0.2, "glow": 0.5 },
  "building": { "model": "building" },
  "airship": { "model": "crane", "providers": { "codex": "crane" } },
  "visitor": { "model": "crane", "path": "glide", "duration": 8 }
}`

// Files is a pack's files, from a document and a manifest.
func Files(t testing.TB, d Doc, manifest string) map[string][]byte {
	t.Helper()
	return map[string][]byte{
		"theme.json": []byte(manifest),
		"models.glb": d.GLB(t),
		"LICENSE":    []byte("CC0 1.0 Universal\n"),
	}
}
