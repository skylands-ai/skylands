package themepack

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/skylands-ai/skylands/internal/themepack/packtest"
)

// gltf is a glTF document under test.
type gltf = packtest.Doc

func testDoc() gltf { return packtest.NewDoc() }

const testManifest = packtest.Manifest

func testFiles(t *testing.T, d gltf, manifest string) map[string][]byte {
	t.Helper()
	return packtest.Files(t, d, manifest)
}

func mustCheck(t *testing.T, files map[string][]byte) *Pack {
	t.Helper()
	p, err := Check(files)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return p
}

// refused checks files and wants every one of want among the problems.
func refused(t *testing.T, files map[string][]byte, want ...string) {
	t.Helper()
	_, err := Check(files)
	if err == nil {
		t.Fatalf("Check took a pack it should refuse; want %q", want)
	}
	if !IsInvalid(err) {
		t.Fatalf("Check: %v, not an InvalidError", err)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("Check's problems don't say %q:\n%v", w, err)
		}
	}
}

func TestCheckTakesAValidPack(t *testing.T) {
	p := mustCheck(t, testFiles(t, testDoc(), testManifest))
	if p.Manifest.ID != "test-pack" || p.Manifest.Name != "Test Pack" {
		t.Errorf("manifest = %+v", p.Manifest)
	}
	if p.Triangles != 1 {
		t.Errorf("triangles = %d, want 1", p.Triangles)
	}
	if len(p.Hash) != 64 {
		t.Errorf("hash = %q", p.Hash)
	}
}

func TestHashIsWhatSha256sumPrints(t *testing.T) {
	files := map[string][]byte{LicenseFile: []byte("a\n"), ModelsFile: []byte("b\n"), ManifestFile: []byte("c\n")}
	// printf 'a\n' > LICENSE; printf 'b\n' > models.glb; printf 'c\n' > theme.json
	// sha256sum LICENSE models.glb theme.json | sha256sum
	const want = "6dc95b4955f3fce63dff7d35414dd344c6adb24f13ecd53a1c94724c3e0d9399"
	got := Hash(files)
	if got != want {
		t.Fatalf("Hash = %s, want %s", got, want)
	}
	// The hash depends on every file and on nothing else.
	for _, name := range Files {
		changed := map[string][]byte{}
		for k, v := range files {
			changed[k] = v
		}
		changed[name] = []byte("x\n")
		if Hash(changed) == got {
			t.Errorf("changing %s left the hash as it was", name)
		}
	}
}

func TestCheckRefusesWhatReachesOutside(t *testing.T) {
	t.Run("a buffer at a URI", func(t *testing.T) {
		d := testDoc()
		d["buffers"] = []any{map[string]any{"byteLength": 36, "uri": "https://example.com/steal.bin"}}
		refused(t, testFiles(t, d, testManifest), "names a URI")
	})
	t.Run("an image at a URI", func(t *testing.T) {
		d := testDoc()
		d["images"] = []any{map[string]any{"uri": "https://example.com/pixel.png"}}
		refused(t, testFiles(t, d, testManifest), "names a URI")
	})
	t.Run("a data URI", func(t *testing.T) {
		d := testDoc()
		d["images"] = []any{map[string]any{"uri": "data:image/png;base64,AAAA"}}
		refused(t, testFiles(t, d, testManifest), "names a URI")
	})
	t.Run("an extension", func(t *testing.T) {
		d := testDoc()
		d["extensionsUsed"] = []any{"KHR_materials_unlit"}
		d["materials"] = []any{map[string]any{"name": "paper", "extensions": map[string]any{"KHR_materials_unlit": map[string]any{}}}}
		refused(t, testFiles(t, d, testManifest), "extensions")
	})
	t.Run("a camera", func(t *testing.T) {
		d := testDoc()
		d["cameras"] = []any{map[string]any{"type": "perspective"}}
		refused(t, testFiles(t, d, testManifest), `"cameras"`)
	})
	t.Run("a skin", func(t *testing.T) {
		d := testDoc()
		nodes := d["nodes"].([]any)
		nodes[11].(map[string]any)["skin"] = 0
		refused(t, testFiles(t, d, testManifest), `"skin"`)
	})
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withImage adds an image in the binary chunk to a document.
func withImage(d gltf, img []byte, mime string) gltf {
	bin := d["_bin"].([]byte)
	off := len(bin)
	bin = append(bin, img...)
	d["_bin"] = bin
	d["buffers"] = []any{map[string]any{"byteLength": len(bin)}}
	d["bufferViews"] = append(d["bufferViews"].([]any), map[string]any{"buffer": 0, "byteOffset": off, "byteLength": len(img)})
	d["images"] = []any{map[string]any{"bufferView": 1, "mimeType": mime}}
	d["textures"] = []any{map[string]any{"source": 0}}
	return d
}

func TestCheckImages(t *testing.T) {
	mustCheck(t, testFiles(t, withImage(testDoc(), pngOf(t, 64, 64), "image/png"), testManifest))
	refused(t, testFiles(t, withImage(testDoc(), pngOf(t, MaxImageSide+1, 8), "image/png"), testManifest), "at most 1024 on a side")
	refused(t, testFiles(t, withImage(testDoc(), pngOf(t, 8, 8), "image/jpeg"), testManifest), "says it is image/jpeg but is png")
	refused(t, testFiles(t, withImage(testDoc(), []byte("<svg/>"), "image/svg+xml"), testManifest), "PNG or JPEG")
}

func TestCheckRefusesBrokenModels(t *testing.T) {
	t.Run("a view past its buffer", func(t *testing.T) {
		d := testDoc()
		d["bufferViews"] = []any{map[string]any{"buffer": 0, "byteLength": 4096}}
		refused(t, testFiles(t, d, testManifest), "runs past its buffer")
	})
	t.Run("lines", func(t *testing.T) {
		d := testDoc()
		d["meshes"] = []any{map[string]any{"primitives": []any{map[string]any{"attributes": map[string]any{"POSITION": 0}, "mode": 1}}}}
		refused(t, testFiles(t, d, testManifest), "isn't triangles")
	})
	t.Run("a loop", func(t *testing.T) {
		d := testDoc()
		nodes := d["nodes"].([]any)
		nodes[1].(map[string]any)["children"] = []any{0}
		refused(t, testFiles(t, d, testManifest))
	})
	t.Run("not a GLB", func(t *testing.T) {
		files := testFiles(t, testDoc(), testManifest)
		files[ModelsFile] = []byte(`{"asset":{"version":"2.0"}}`)
		refused(t, files, "isn't a GLB")
	})
	t.Run("too many triangles", func(t *testing.T) {
		d := testDoc()
		d["accessors"] = []any{map[string]any{"bufferView": 0, "componentType": 5126, "count": 3, "type": "VEC3"}, map[string]any{"componentType": 5125, "count": (MaxTriangles + 1) * 3, "type": "SCALAR"}}
		d["meshes"] = []any{map[string]any{"primitives": []any{map[string]any{"attributes": map[string]any{"POSITION": 0}, "indices": 1}}}}
		refused(t, testFiles(t, d, testManifest), "triangles; a pack may have at most")
	})
}

func TestCheckWantsTheModelsTheManifestNames(t *testing.T) {
	t.Run("a model that isn't there", func(t *testing.T) {
		m := strings.Replace(testManifest, `"model": "crane", "providers"`, `"model": "heron", "providers"`, 1)
		refused(t, testFiles(t, testDoc(), m), `airship.model is "heron", but models.glb's scene has no root node by that name`)
	})
	t.Run("a citadel without its beacon", func(t *testing.T) {
		d := testDoc()
		d["nodes"].([]any)[3].(map[string]any)["name"] = "lamp"
		refused(t, testFiles(t, d, testManifest), `citadel.model "citadel" has no part named beacon`)
	})
	t.Run("a building of two materials", func(t *testing.T) {
		d := testDoc()
		d["meshes"] = append(d["meshes"].([]any), map[string]any{"primitives": []any{
			map[string]any{"attributes": map[string]any{"POSITION": 0}},
			map[string]any{"attributes": map[string]any{"POSITION": 0}},
		}})
		d["nodes"].([]any)[9].(map[string]any)["mesh"] = 1
		refused(t, testFiles(t, d, testManifest), "must be one mesh with one material")
	})
	t.Run("two roots by one name", func(t *testing.T) {
		d := testDoc()
		d["nodes"].([]any)[8].(map[string]any)["name"] = "plaza"
		refused(t, testFiles(t, d, testManifest), `has 2 root nodes by that name`)
	})
}

func TestCheckManifest(t *testing.T) {
	cases := []struct {
		name, from, to, want string
	}{
		{"a built-in id", `"id": "test-pack"`, `"id": "scifi"`, "built-in"},
		{"a bad id", `"id": "test-pack"`, `"id": "Test Pack"`, "lowercase"},
		{"a later format", `"format": 1`, `"format": 2`, "reads format 1"},
		{"a bad color", `"beacon": "#ffd27a"`, `"beacon": "gold"`, "#rrggbb"},
		{"a number out of range", `"spacing": 1,`, `"spacing": 0,`, "ring.spacing"},
		{"two states alike", `"rest": { "color": "#eeeeee" }`, `"rest": { "color": "#eeffff", "emissive": "#33ccdd", "emissiveIntensity": 0.5 }`, "couldn't tell the states apart"},
		{"an unknown provider", `"codex": "crane"`, `"copilot": "crane"`, "copilot"},
		{"an unknown path", `"path": "glide"`, `"path": "teleport"`, "visitor.path"},
		{"a field it doesn't know", `"format": 1,`, `"format": 1, "script": "steal()",`, "unknown field"},
		{"no license", `"license": "CC0-1.0"`, `"license": ""`, "SPDX"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := strings.Replace(testManifest, c.from, c.to, 1)
			if m == testManifest {
				t.Fatalf("%q isn't in the test manifest", c.from)
			}
			refused(t, testFiles(t, testDoc(), m), c.want)
		})
	}
}

// withRope gives the test manifest's terrace a rope.
func withRope(rope string) string {
	return strings.Replace(testManifest, `"glow": 0.5 }`, `"glow": 0.5, "rope": `+rope+` }`, 1)
}

// withBigModel adds a root named name of 65 triangles to a document.
func withBigModel(d gltf, name string) gltf {
	idx := make([]byte, 65*3*2)
	for i := range 65 * 3 {
		idx[i*2] = byte(i % 3)
	}
	bin := append(d["_bin"].([]byte), idx...)
	d["_bin"] = bin
	d["buffers"] = []any{map[string]any{"byteLength": len(bin)}}
	d["bufferViews"] = append(d["bufferViews"].([]any), map[string]any{"buffer": 0, "byteOffset": 36, "byteLength": len(idx)})
	d["accessors"] = append(d["accessors"].([]any), map[string]any{"bufferView": 1, "componentType": 5123, "count": 65 * 3, "type": "SCALAR"})
	d["meshes"] = append(d["meshes"].([]any), map[string]any{"primitives": []any{map[string]any{"attributes": map[string]any{"POSITION": 0}, "indices": 1, "material": 0}}})
	d["nodes"] = append(d["nodes"].([]any), map[string]any{"name": name, "mesh": 1})
	scene := d["scenes"].([]any)[0].(map[string]any)
	scene["nodes"] = append(scene["nodes"].([]any), len(d["nodes"].([]any))-1)
	return d
}

func TestCheckRope(t *testing.T) {
	const rope = `{ "at": [0, 0.8, 0.1], "sag": 0.3, "radius": 0.02, "material": "paper", "bead": { "model": "ring", "spacing": 0.2 } }`
	t.Run("a rope with beads", func(t *testing.T) {
		p := mustCheck(t, testFiles(t, testDoc(), withRope(rope)))
		r := p.Manifest.Terrace.Rope
		if r == nil || r.Material != "paper" || r.Bead == nil || r.Bead.Model != "ring" {
			t.Errorf("rope = %+v", r)
		}
	})
	t.Run("a material named as Blender names a second one", func(t *testing.T) {
		mustCheck(t, testFiles(t, testDoc(), withRope(strings.Replace(rope, `"paper"`, `"paper.001"`, 1))))
	})
	t.Run("a rope without beads", func(t *testing.T) {
		mustCheck(t, testFiles(t, testDoc(), withRope(`{ "at": [0, 0.8, 0], "sag": 0, "radius": 0.02, "material": "paper" }`)))
	})
	t.Run("a material no model uses", func(t *testing.T) {
		d := testDoc()
		d["materials"] = append(d["materials"].([]any), map[string]any{"name": "string"})
		refused(t, testFiles(t, d, withRope(strings.Replace(rope, `"paper"`, `"string"`, 1))), `terrace.rope.material is "string", but no model`)
	})
	t.Run("a bead that isn't there", func(t *testing.T) {
		refused(t, testFiles(t, testDoc(), withRope(strings.Replace(rope, `"ring"`, `"bulb"`, 1))), `terrace.rope.bead.model is "bulb", but models.glb's scene has no root node`)
	})
	t.Run("a bead of too many triangles", func(t *testing.T) {
		d := withBigModel(testDoc(), "bulb")
		refused(t, testFiles(t, d, withRope(strings.Replace(rope, `"ring"`, `"bulb"`, 1))), `"bulb" has 65 triangles; a bead may have at most 64`)
	})
	cases := []struct {
		name, from, to, want string
	}{
		{"a point of two numbers", `[0, 0.8, 0.1]`, `[0, 0.8]`, "terrace.rope.at has 2 numbers"},
		{"a point under the island", `[0, 0.8, 0.1]`, `[0, -1, 0.1]`, "terrace.rope.at[1]"},
		{"too much sag", `"sag": 0.3`, `"sag": 3`, "terrace.rope.sag"},
		{"no thickness", `"radius": 0.02`, `"radius": 0`, "terrace.rope.radius"},
		{"no material", `"material": "paper", `, ``, "terrace.rope.material is missing"},
		{"beads too close", `"spacing": 0.2`, `"spacing": 0.01`, "terrace.rope.bead.spacing"},
		{"a field it doesn't know", `"sag": 0.3`, `"sag": 0.3, "sway": 1`, "unknown field"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := strings.Replace(rope, c.from, c.to, 1)
			if r == rope {
				t.Fatalf("%q isn't in the test rope", c.from)
			}
			refused(t, testFiles(t, testDoc(), withRope(r)), c.want)
		})
	}
}

func TestCheckWantsEveryFileAndNoOthers(t *testing.T) {
	files := testFiles(t, testDoc(), testManifest)
	delete(files, LicenseFile)
	refused(t, files, "has no LICENSE")
	files = testFiles(t, testDoc(), testManifest)
	files["script.js"] = []byte("alert(1)")
	refused(t, files, "script.js")
}

func TestArchiveReadsBack(t *testing.T) {
	p := mustCheck(t, testFiles(t, testDoc(), testManifest))
	a, err := p.Archive()
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Archive()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("the same pack zipped twice gives different bytes")
	}
	files, others, err := ReadZip(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(others) != 0 {
		t.Errorf("others = %v", others)
	}
	if got := mustCheck(t, files); got.Hash != p.Hash {
		t.Errorf("hash after a round trip = %s, want %s", got.Hash, p.Hash)
	}
}
