package themepack

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // models.glb may hold JPEG textures
	_ "image/png"  // and PNG ones
	"slices"
	"strings"
)

// Limits on what models.glb may hold. They keep a pack quick to load and
// draw on a phone, and keep what a browser parses small.
const (
	MaxTriangles  = 100_000
	MaxNodes      = 4096
	MaxImages     = 16
	MaxImageSide  = 1024
	MaxAnimations = 64
	maxAccessor   = 1 << 20
)

const (
	glbMagic  = 0x46546C67 // "glTF"
	chunkJSON = 0x4E4F534A
	chunkBIN  = 0x004E4942
)

// The parts of glTF a pack may use. Anything else is refused: cameras,
// skins, morph targets, sparse accessors, and every extension, since the
// world neither needs them nor checks them.
var (
	topKeys       = []string{"asset", "scene", "scenes", "nodes", "meshes", "materials", "accessors", "bufferViews", "buffers", "animations", "images", "textures", "samplers", "extras", "extensionsUsed", "extensionsRequired"}
	nodeKeys      = []string{"name", "children", "mesh", "matrix", "translation", "rotation", "scale", "extras"}
	primitiveKeys = []string{"attributes", "indices", "material", "mode", "extras"}
	accessorKeys  = []string{"bufferView", "byteOffset", "componentType", "normalized", "count", "type", "max", "min", "name", "extras"}
	attributes    = []string{"POSITION", "NORMAL", "TANGENT", "TEXCOORD_0", "TEXCOORD_1", "COLOR_0"}
	paths         = []string{"translation", "rotation", "scale"}
	interpolation = []string{"", "LINEAR", "STEP", "CUBICSPLINE"}
	imageTypes    = map[string]string{"image/png": "png", "image/jpeg": "jpeg"}
)

var componentSize = map[int]int{5120: 1, 5121: 1, 5122: 2, 5123: 2, 5125: 4, 5126: 4}

var typeComponents = map[string]int{"SCALAR": 1, "VEC2": 2, "VEC3": 3, "VEC4": 4, "MAT2": 4, "MAT3": 9, "MAT4": 16}

type gltfNode struct {
	Name     string `json:"name"`
	Children []int  `json:"children"`
	Mesh     *int   `json:"mesh"`
}

type gltfPrimitive struct {
	Attributes map[string]int `json:"attributes"`
	Indices    *int           `json:"indices"`
	Material   *int           `json:"material"`
	Mode       *int           `json:"mode"`
}

type gltfAccessor struct {
	BufferView    *int   `json:"bufferView"`
	ByteOffset    int    `json:"byteOffset"`
	ComponentType int    `json:"componentType"`
	Count         int    `json:"count"`
	Type          string `json:"type"`
}

type gltfBufferView struct {
	Buffer     int `json:"buffer"`
	ByteOffset int `json:"byteOffset"`
	ByteLength int `json:"byteLength"`
	ByteStride int `json:"byteStride"`
}

type gltfAnimation struct {
	Channels []struct {
		Sampler int `json:"sampler"`
		Target  struct {
			Node *int   `json:"node"`
			Path string `json:"path"`
		} `json:"target"`
	} `json:"channels"`
	Samplers []struct {
		Input         int    `json:"input"`
		Output        int    `json:"output"`
		Interpolation string `json:"interpolation"`
	} `json:"samplers"`
}

type gltfDoc struct {
	Asset struct {
		Version string `json:"version"`
	} `json:"asset"`
	Scene  *int `json:"scene"`
	Scenes []struct {
		Nodes []int `json:"nodes"`
	} `json:"scenes"`
	Nodes  []gltfNode `json:"nodes"`
	Meshes []struct {
		Primitives []gltfPrimitive `json:"primitives"`
	} `json:"meshes"`
	Materials   []json.RawMessage `json:"materials"`
	Accessors   []gltfAccessor    `json:"accessors"`
	BufferViews []gltfBufferView  `json:"bufferViews"`
	Buffers     []struct {
		ByteLength int `json:"byteLength"`
	} `json:"buffers"`
	Animations []gltfAnimation `json:"animations"`
	Images     []struct {
		BufferView *int   `json:"bufferView"`
		MimeType   string `json:"mimeType"`
	} `json:"images"`
	Textures []struct {
		Source  *int `json:"source"`
		Sampler *int `json:"sampler"`
	} `json:"textures"`
	Samplers []json.RawMessage `json:"samplers"`
}

// models is what checkGLB found in models.glb.
type models struct {
	doc       gltfDoc
	roots     []int
	triangles int
}

// splitGLB returns a GLB's JSON and binary chunks.
func splitGLB(data []byte) (jsonChunk, bin []byte, err error) {
	if len(data) < 20 {
		return nil, nil, fmt.Errorf("models.glb is too short to be a GLB")
	}
	if binary.LittleEndian.Uint32(data[0:]) != glbMagic {
		return nil, nil, fmt.Errorf("models.glb isn't a GLB file")
	}
	if v := binary.LittleEndian.Uint32(data[4:]); v != 2 {
		return nil, nil, fmt.Errorf("models.glb is GLB version %d, not 2", v)
	}
	if n := binary.LittleEndian.Uint32(data[8:]); int(n) != len(data) {
		return nil, nil, fmt.Errorf("models.glb says it is %d bytes, but it is %d", n, len(data))
	}
	rest := data[12:]
	for i := 0; len(rest) > 0; i++ {
		if len(rest) < 8 {
			return nil, nil, fmt.Errorf("models.glb ends inside a chunk header")
		}
		n := int(binary.LittleEndian.Uint32(rest[0:]))
		kind := binary.LittleEndian.Uint32(rest[4:])
		if n > len(rest)-8 {
			return nil, nil, fmt.Errorf("models.glb's chunk %d runs past the end of the file", i)
		}
		body := rest[8 : 8+n]
		switch {
		case i == 0 && kind == chunkJSON:
			jsonChunk = body
		case i == 0:
			return nil, nil, fmt.Errorf("models.glb's first chunk isn't JSON")
		case i == 1 && kind == chunkBIN:
			bin = body
		case kind == chunkJSON || kind == chunkBIN:
			return nil, nil, fmt.Errorf("models.glb has more than one JSON or binary chunk")
		}
		rest = rest[8+n:]
	}
	return jsonChunk, bin, nil
}

// checkGLB finds what is wrong with models.glb.
func checkGLB(data []byte, p *problems) *models {
	jsonChunk, bin, err := splitGLB(data)
	if err != nil {
		p.add("%v", err)
		return nil
	}
	var raw any
	if err := json.Unmarshal(jsonChunk, &raw); err != nil {
		p.add("models.glb's JSON: %v", err)
		return nil
	}
	top, _ := raw.(map[string]any)
	if top == nil {
		p.add("models.glb's JSON isn't an object")
		return nil
	}
	before := len(*p)
	onlyKeys(p, "models.glb", top, topKeys)
	for _, k := range []string{"extensionsUsed", "extensionsRequired"} {
		if l, ok := top[k].([]any); ok && len(l) > 0 {
			p.add("models.glb lists glTF extensions in %s %v, which packs can't use", k, l)
		}
	}
	// Anything that names a URI could send the browser elsewhere for
	// data, and an extension would mean something no check here knows.
	walk(raw, "models.glb", func(path, key string) {
		switch key {
		case "uri":
			p.add("%s names a URI; everything a pack draws must be inside models.glb", path)
		case "extensions":
			p.add("%s uses glTF extensions, which packs can't", path)
		}
	})
	for i, n := range listOf(top, "nodes") {
		onlyKeys(p, fmt.Sprintf("models.glb node %d", i), n, nodeKeys)
	}
	for i, mesh := range listOf(top, "meshes") {
		prims, _ := mesh["primitives"].([]any)
		for j, prim := range prims {
			if pm, ok := prim.(map[string]any); ok {
				onlyKeys(p, fmt.Sprintf("models.glb mesh %d primitive %d", i, j), pm, primitiveKeys)
			}
		}
	}
	for i, a := range listOf(top, "accessors") {
		onlyKeys(p, fmt.Sprintf("models.glb accessor %d", i), a, accessorKeys)
	}
	if len(*p) > before {
		return nil
	}

	var doc gltfDoc
	if err := json.Unmarshal(jsonChunk, &doc); err != nil {
		p.add("models.glb's JSON: %v", err)
		return nil
	}
	if doc.Asset.Version != "2.0" {
		p.add("models.glb is glTF %q, not 2.0", doc.Asset.Version)
		return nil
	}
	checkBuffers(&doc, bin, p)
	checkImages(&doc, bin, p)
	tris := checkMeshes(&doc, p)
	checkAnimations(&doc, p)
	roots := checkNodes(&doc, p)
	if len(*p) > before {
		return nil
	}
	if tris > MaxTriangles {
		p.add("models.glb has %d triangles; a pack may have at most %d", tris, MaxTriangles)
	}
	return &models{doc: doc, roots: roots, triangles: tris}
}

func onlyKeys(p *problems, what string, obj map[string]any, allowed []string) {
	for k := range obj {
		if !slices.Contains(allowed, k) {
			p.add("%s has %q, which packs can't use", what, k)
		}
	}
}

func listOf(top map[string]any, key string) []map[string]any {
	l, _ := top[key].([]any)
	out := make([]map[string]any, 0, len(l))
	for _, v := range l {
		m, _ := v.(map[string]any)
		out = append(out, m)
	}
	return out
}

// walk calls visit with every object key in v, and where it is.
func walk(v any, path string, visit func(path, key string)) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if k == "extras" {
				// extras are free-form, and the world reads none of them.
				continue
			}
			visit(path, k)
			walk(child, path+"."+k, visit)
		}
	case []any:
		for i, child := range t {
			walk(child, fmt.Sprintf("%s[%d]", path, i), visit)
		}
	}
}

func checkBuffers(doc *gltfDoc, bin []byte, p *problems) {
	if len(doc.Buffers) > 1 {
		p.add("models.glb has %d buffers; a GLB holds one", len(doc.Buffers))
		return
	}
	size := 0
	if len(doc.Buffers) == 1 {
		size = doc.Buffers[0].ByteLength
		if size > len(bin) {
			p.add("models.glb's buffer is %d bytes, longer than its binary chunk", size)
			return
		}
	}
	for i, v := range doc.BufferViews {
		if v.Buffer != 0 || len(doc.Buffers) == 0 {
			p.add("models.glb buffer view %d isn't in its buffer", i)
			continue
		}
		if v.ByteOffset < 0 || v.ByteLength <= 0 || v.ByteOffset+v.ByteLength > size || v.ByteStride < 0 || v.ByteStride > 252 {
			p.add("models.glb buffer view %d runs past its buffer", i)
		}
	}
	for i, a := range doc.Accessors {
		cs, ok := componentSize[a.ComponentType]
		nc, tok := typeComponents[a.Type]
		switch {
		case !ok || !tok:
			p.add("models.glb accessor %d has an unknown component type or type", i)
			continue
		case a.Count < 1 || a.Count > maxAccessor:
			p.add("models.glb accessor %d has %d elements", i, a.Count)
			continue
		case a.BufferView == nil:
			continue
		case *a.BufferView < 0 || *a.BufferView >= len(doc.BufferViews):
			p.add("models.glb accessor %d names a buffer view that isn't there", i)
			continue
		}
		v := doc.BufferViews[*a.BufferView]
		elem := cs * nc
		stride := elem
		if v.ByteStride > 0 {
			stride = v.ByteStride
		}
		if a.ByteOffset < 0 || a.ByteOffset+(a.Count-1)*stride+elem > v.ByteLength {
			p.add("models.glb accessor %d runs past its buffer view", i)
		}
	}
}

func checkImages(doc *gltfDoc, bin []byte, p *problems) {
	if len(doc.Images) > MaxImages {
		p.add("models.glb has %d images; a pack may have at most %d", len(doc.Images), MaxImages)
		return
	}
	for i, img := range doc.Images {
		want, ok := imageTypes[img.MimeType]
		if !ok {
			p.add("models.glb image %d is %q; images are PNG or JPEG", i, img.MimeType)
			continue
		}
		if img.BufferView == nil || *img.BufferView < 0 || *img.BufferView >= len(doc.BufferViews) {
			p.add("models.glb image %d isn't in models.glb", i)
			continue
		}
		v := doc.BufferViews[*img.BufferView]
		if v.ByteOffset+v.ByteLength > len(bin) {
			continue // reported with the buffer views
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(bin[v.ByteOffset : v.ByteOffset+v.ByteLength]))
		switch {
		case err != nil:
			p.add("models.glb image %d: %v", i, err)
		case format != want:
			p.add("models.glb image %d says it is %s but is %s", i, img.MimeType, format)
		case cfg.Width > MaxImageSide || cfg.Height > MaxImageSide:
			p.add("models.glb image %d is %d by %d; images are at most %d on a side", i, cfg.Width, cfg.Height, MaxImageSide)
		}
	}
	for i, t := range doc.Textures {
		if t.Source == nil || *t.Source < 0 || *t.Source >= len(doc.Images) {
			p.add("models.glb texture %d names an image that isn't there", i)
		}
		if t.Sampler != nil && (*t.Sampler < 0 || *t.Sampler >= len(doc.Samplers)) {
			p.add("models.glb texture %d names a sampler that isn't there", i)
		}
	}
}

func (doc *gltfDoc) accessor(i int) bool { return i >= 0 && i < len(doc.Accessors) }

func checkMeshes(doc *gltfDoc, p *problems) int {
	tris := 0
	for i, m := range doc.Meshes {
		if len(m.Primitives) == 0 {
			p.add("models.glb mesh %d has no primitives", i)
		}
		for j, prim := range m.Primitives {
			what := fmt.Sprintf("models.glb mesh %d primitive %d", i, j)
			if prim.Mode != nil && *prim.Mode != 4 {
				p.add("%s isn't triangles", what)
			}
			for name, a := range prim.Attributes {
				if !slices.Contains(attributes, name) {
					p.add("%s has attribute %s, which packs can't use", what, name)
				} else if !doc.accessor(a) {
					p.add("%s's %s names an accessor that isn't there", what, name)
				}
			}
			pos, ok := prim.Attributes["POSITION"]
			if !ok {
				p.add("%s has no POSITION", what)
				continue
			}
			if prim.Material != nil && (*prim.Material < 0 || *prim.Material >= len(doc.Materials)) {
				p.add("%s names a material that isn't there", what)
			}
			switch {
			case prim.Indices != nil && !doc.accessor(*prim.Indices):
				p.add("%s's indices name an accessor that isn't there", what)
			case prim.Indices != nil:
				tris += doc.Accessors[*prim.Indices].Count / 3
			case doc.accessor(pos):
				tris += doc.Accessors[pos].Count / 3
			}
		}
	}
	return tris
}

func checkAnimations(doc *gltfDoc, p *problems) {
	if len(doc.Animations) > MaxAnimations {
		p.add("models.glb has %d animations; a pack may have at most %d", len(doc.Animations), MaxAnimations)
		return
	}
	for i, a := range doc.Animations {
		what := fmt.Sprintf("models.glb animation %d", i)
		for _, s := range a.Samplers {
			if !doc.accessor(s.Input) || !doc.accessor(s.Output) {
				p.add("%s has a sampler whose accessors aren't there", what)
			}
			if !slices.Contains(interpolation, s.Interpolation) {
				p.add("%s interpolates by %q", what, s.Interpolation)
			}
		}
		for _, c := range a.Channels {
			switch {
			case c.Sampler < 0 || c.Sampler >= len(a.Samplers):
				p.add("%s has a channel whose sampler isn't there", what)
			case c.Target.Node == nil || *c.Target.Node < 0 || *c.Target.Node >= len(doc.Nodes):
				p.add("%s has a channel whose node isn't there", what)
			case !slices.Contains(paths, c.Target.Path):
				p.add("%s moves %q; packs animate translation, rotation, and scale", what, c.Target.Path)
			}
		}
	}
}

// checkNodes checks the node tree and returns the default scene's roots.
func checkNodes(doc *gltfDoc, p *problems) []int {
	if len(doc.Nodes) > MaxNodes {
		p.add("models.glb has %d nodes; a pack may have at most %d", len(doc.Nodes), MaxNodes)
		return nil
	}
	parent := make([]int, len(doc.Nodes))
	for i := range parent {
		parent[i] = -1
	}
	for i, n := range doc.Nodes {
		if n.Mesh != nil && (*n.Mesh < 0 || *n.Mesh >= len(doc.Meshes)) {
			p.add("models.glb node %d names a mesh that isn't there", i)
		}
		for _, c := range n.Children {
			switch {
			case c < 0 || c >= len(doc.Nodes) || c == i:
				p.add("models.glb node %d has a child that isn't there", i)
			case parent[c] >= 0:
				p.add("models.glb node %d has more than one parent", c)
			default:
				parent[c] = i
			}
		}
	}
	// A node that is its own ancestor would loop forever.
	for i := range doc.Nodes {
		steps := 0
		for at := parent[i]; at >= 0; at = parent[at] {
			if steps++; steps > len(doc.Nodes) {
				p.add("models.glb's nodes form a loop")
				return nil
			}
		}
	}
	if len(doc.Scenes) == 0 {
		p.add("models.glb has no scene")
		return nil
	}
	scene := 0
	if doc.Scene != nil {
		scene = *doc.Scene
	}
	if scene < 0 || scene >= len(doc.Scenes) {
		p.add("models.glb's default scene isn't there")
		return nil
	}
	roots := doc.Scenes[scene].Nodes
	for _, r := range roots {
		if r < 0 || r >= len(doc.Nodes) || parent[r] >= 0 {
			p.add("models.glb's scene names a node that isn't a root")
			return nil
		}
	}
	return roots
}

// stem is a name up to its first dot, so core.001, as Blender names a
// second core, is a core too.
func stem(name string) string {
	s, _, _ := strings.Cut(name, ".")
	return s
}

// checkModels finds what the manifest names that models.glb lacks.
func checkModels(m *Manifest, ms *models, p *problems) {
	byName := map[string][]int{}
	for _, r := range ms.roots {
		byName[ms.doc.Nodes[r].Name] = append(byName[ms.doc.Nodes[r].Name], r)
	}
	for _, use := range m.models() {
		found := byName[use.name]
		switch len(found) {
		case 0:
			p.add("%s is %q, but models.glb's scene has no root node by that name", use.field, use.name)
			continue
		case 1:
		default:
			p.add("%s is %q, but models.glb's scene has %d root nodes by that name", use.field, use.name, len(found))
			continue
		}
		root := found[0]
		for _, part := range use.parts {
			if !ms.hasPart(root, part) {
				p.add("%s %q has no part named %s", use.field, use.name, part)
			}
		}
		if use.soleMesh {
			n := ms.doc.Nodes[root]
			if n.Mesh == nil || len(ms.doc.Meshes[*n.Mesh].Primitives) != 1 {
				p.add("%s %q must be one mesh with one material, which the world dresses in each state", use.field, use.name)
			}
		}
	}
}

// hasPart says whether a node below root has part as its name's stem.
func (ms *models) hasPart(root int, part string) bool {
	for _, c := range ms.doc.Nodes[root].Children {
		if stem(ms.doc.Nodes[c].Name) == part || ms.hasPart(c, part) {
			return true
		}
	}
	return false
}
