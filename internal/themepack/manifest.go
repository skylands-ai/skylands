package themepack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

// Format is the version of the manifest format this package reads.
const Format = 1

// BuiltIn is the ids of the themes the web client draws in code, which no
// pack may take.
var BuiltIn = []string{"default", "scifi"}

// Providers is who runs an agent, each of which can fly its own airship.
var Providers = []string{"claude", "codex", "gemini", "goose", "local"}

// VisitorPaths is the ways across the view the world can send a visitor.
var VisitorPaths = []string{"glide"}

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	licensePattern = regexp.MustCompile(`^[A-Za-z0-9.+-]+( (AND|OR|WITH) [A-Za-z0-9.+-]+)*$`)
	colorPattern   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// Manifest is a pack's theme.json: what it is, how its sky and buildings
// look, and which of its models the world draws each thing with.
type Manifest struct {
	// Schema lets an editor find the JSON schema. Nothing reads it.
	Schema      string `json:"$schema,omitempty"`
	Format      int    `json:"format"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	// License is the SPDX expression for the pack's models, whose text is
	// in its LICENSE file.
	License   string        `json:"license"`
	Sky       *Sky          `json:"sky"`
	Buildings BuildingLooks `json:"buildings"`
	Citadel   Citadel       `json:"citadel"`
	Island    Island        `json:"island"`
	Ring      Ring          `json:"ring"`
	Terrace   Terrace       `json:"terrace"`
	Building  Building      `json:"building"`
	Airship   Airship       `json:"airship"`
	Visitor   *Visitor      `json:"visitor,omitempty"`
}

// Color is a CSS hex color, #rrggbb.
type Color string

// Sky is how the sky looks. A null sky follows the page's light or dark
// colors, as the default theme's does.
type Sky struct {
	Top           Color   `json:"top"`
	Horizon       Color   `json:"horizon"`
	Fog           Color   `json:"fog"`
	FogNear       float64 `json:"fogNear"`
	FogFar        float64 `json:"fogFar"`
	Stars         float64 `json:"stars"`
	StarsAround   bool    `json:"starsAround"`
	Clouds        bool    `json:"clouds"`
	HemiSky       Color   `json:"hemiSky"`
	HemiGround    Color   `json:"hemiGround"`
	HemiIntensity float64 `json:"hemiIntensity"`
	Sun           Color   `json:"sun"`
	SunIntensity  float64 `json:"sunIntensity"`
}

// Material is a standard material: a color, a glow, and how rough and
// metallic its surface is.
type Material struct {
	Color             Color    `json:"color"`
	Emissive          Color    `json:"emissive,omitempty"`
	EmissiveIntensity float64  `json:"emissiveIntensity,omitempty"`
	Roughness         *float64 `json:"roughness,omitempty"`
	Metalness         *float64 `json:"metalness,omitempty"`
}

// BuildingLooks is what a building wears in each state. The world tells
// the states apart by these, so each must be its own.
type BuildingLooks struct {
	Rest     Material `json:"rest"`
	Read     Material `json:"read"`
	Edit     Material `json:"edit"`
	Conflict Material `json:"conflict"`
	Selected Material `json:"selected"`
}

// Citadel is the model the citadel is drawn with.
type Citadel struct {
	Model string `json:"model"`
	// LabelY is how high above the citadel's origin its label floats.
	LabelY float64 `json:"labelY"`
	// RingRadius is the radius of its selection ring.
	RingRadius float64 `json:"ringRadius"`
	// Beacon is the color of the light at its beacon.
	Beacon Color `json:"beacon"`
}

// Island is the models an island is drawn with, one picked for each
// island, and the scenery scattered on it.
type Island struct {
	Models  []string  `json:"models"`
	Scenery []Scenery `json:"scenery,omitempty"`
}

// Scenery is a model scattered on islands, as many per unit of radius as
// PerRadius, hidden where a terrace covers it with Pad room around.
type Scenery struct {
	Model     string     `json:"model"`
	PerRadius float64    `json:"perRadius"`
	Pad       float64    `json:"pad"`
	Scale     [2]float64 `json:"scale"`
}

// Ring is the piece repeated round a selection ring, about Spacing apart,
// and how fast the ring turns, in radians a second.
type Ring struct {
	Model   string  `json:"model"`
	Spacing float64 `json:"spacing"`
	Spin    float64 `json:"spin"`
}

// Terrace is a terrace's plaza and the post repeated around it, about
// PostSpacing apart and PostOut outside the plaza's edge, with a Rope from
// each post to the next if there is one. Glow is how brightly its
// repo-glow material glows at rest.
type Terrace struct {
	Plaza       string  `json:"plaza"`
	Post        string  `json:"post"`
	PostSpacing float64 `json:"postSpacing"`
	PostOut     float64 `json:"postOut"`
	Glow        float64 `json:"glow"`
	Rope        *Rope   `json:"rope,omitempty"`
}

// Rope is what the world hangs from each of a terrace's posts to the next:
// a tube Radius thick from the point At in each post's own space, its
// middle Sag below a straight line, drawn in the material of models.glb
// named Material, with a Bead strung along it if there is one.
type Rope struct {
	At       []float64 `json:"at"`
	Sag      float64   `json:"sag"`
	Radius   float64   `json:"radius"`
	Material string    `json:"material"`
	Bead     *Bead     `json:"bead,omitempty"`
}

// Bead is a model the world strings along a rope, about Spacing apart.
type Bead struct {
	Model   string  `json:"model"`
	Spacing float64 `json:"spacing"`
}

// MaxBeadTriangles is how many triangles a bead may have. A terrace
// strings hundreds of them.
const MaxBeadTriangles = 64

// Building is the model of one file's building.
type Building struct {
	Model string `json:"model"`
}

// Airship is the model every agent's airship is drawn with, unless
// Providers names one for its provider.
type Airship struct {
	Model     string            `json:"model"`
	Providers map[string]string `json:"providers,omitempty"`
}

// Visitor is what crosses the view now and then, by which path, in how
// many seconds.
type Visitor struct {
	Model    string  `json:"model"`
	Path     string  `json:"path"`
	Duration float64 `json:"duration"`
}

// ParseManifest reads a theme.json. It refuses fields it doesn't know,
// since a manifest that means more than this package reads must not be
// drawn as if it meant less.
func ParseManifest(data []byte) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("theme.json: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("theme.json: more than one JSON value")
	}
	return &m, nil
}

// problems collects what is wrong with a pack, so one check reports all
// of it.
type problems []string

func (p *problems) add(format string, args ...any) { *p = append(*p, fmt.Sprintf(format, args...)) }

func (p *problems) number(field string, v, lo, hi float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < lo || v > hi {
		p.add("%s is %v, which isn't from %v to %v", field, v, lo, hi)
	}
}

func (p *problems) color(field string, c Color, optional bool) {
	if c == "" && optional {
		return
	}
	if !colorPattern.MatchString(string(c)) {
		p.add("%s is %q, which isn't a #rrggbb color", field, c)
	}
}

func (p *problems) text(field, v string, required bool, max int) {
	switch {
	case v == "" && required:
		p.add("%s is missing", field)
	case len(v) > max:
		p.add("%s is longer than %d bytes", field, max)
	case strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		p.add("%s has control characters", field)
	}
}

func (p *problems) model(field, name string) {
	switch {
	case name == "":
		p.add("%s is missing", field)
	case len(name) > 64:
		p.add("%s is longer than 64 bytes", field)
	}
}

func (p *problems) material(field string, m Material) {
	p.color(field+".color", m.Color, false)
	p.color(field+".emissive", m.Emissive, true)
	p.number(field+".emissiveIntensity", m.EmissiveIntensity, 0, 4)
	if m.Roughness != nil {
		p.number(field+".roughness", *m.Roughness, 0, 1)
	}
	if m.Metalness != nil {
		p.number(field+".metalness", *m.Metalness, 0, 1)
	}
}

// check finds what is wrong with the manifest on its own, apart from
// whether its models are in models.glb.
func (m *Manifest) check(p *problems) {
	if m.Format != Format {
		p.add("format is %d; this version of Skylands reads format %d", m.Format, Format)
		return
	}
	switch {
	case !idPattern.MatchString(m.ID) || len(m.ID) < 2 || len(m.ID) > 40:
		p.add("id is %q; an id is 2 to 40 lowercase letters, digits, and single hyphens", m.ID)
	case slices.Contains(BuiltIn, m.ID):
		p.add("id %q is a built-in theme's", m.ID)
	}
	p.text("name", m.Name, true, 40)
	if !versionPattern.MatchString(m.Version) {
		p.add("version is %q, which isn't MAJOR.MINOR.PATCH", m.Version)
	}
	p.text("description", m.Description, false, 200)
	p.text("author", m.Author, false, 80)
	if !licensePattern.MatchString(m.License) || len(m.License) > 64 {
		p.add("license is %q, which isn't an SPDX license expression", m.License)
	}
	if s := m.Sky; s != nil {
		p.color("sky.top", s.Top, false)
		p.color("sky.horizon", s.Horizon, false)
		p.color("sky.fog", s.Fog, false)
		p.number("sky.fogNear", s.FogNear, 0, 1000)
		p.number("sky.fogFar", s.FogFar, 1, 2000)
		if s.FogFar <= s.FogNear {
			p.add("sky.fogFar must be past sky.fogNear")
		}
		p.number("sky.stars", s.Stars, 0, 1)
		p.color("sky.hemiSky", s.HemiSky, false)
		p.color("sky.hemiGround", s.HemiGround, false)
		p.number("sky.hemiIntensity", s.HemiIntensity, 0, 4)
		p.color("sky.sun", s.Sun, false)
		p.number("sky.sunIntensity", s.SunIntensity, 0, 4)
	}
	b := m.Buildings
	looks := []struct {
		name string
		m    Material
	}{{"rest", b.Rest}, {"read", b.Read}, {"edit", b.Edit}, {"conflict", b.Conflict}, {"selected", b.Selected}}
	for i, l := range looks {
		p.material("buildings."+l.name, l.m)
		for _, o := range looks[:i] {
			if l.m.Color == o.m.Color && l.m.Emissive == o.m.Emissive && l.m.EmissiveIntensity == o.m.EmissiveIntensity {
				p.add("buildings.%s looks the same as buildings.%s, so the world couldn't tell the states apart", l.name, o.name)
			}
		}
	}
	p.model("citadel.model", m.Citadel.Model)
	p.number("citadel.labelY", m.Citadel.LabelY, 0, 40)
	p.number("citadel.ringRadius", m.Citadel.RingRadius, 2, 20)
	p.color("citadel.beacon", m.Citadel.Beacon, false)
	if n := len(m.Island.Models); n < 1 || n > 8 {
		p.add("island.models has %d models; it takes 1 to 8", n)
	}
	for i, name := range m.Island.Models {
		p.model(fmt.Sprintf("island.models[%d]", i), name)
	}
	if len(m.Island.Scenery) > 8 {
		p.add("island.scenery has %d kinds; it takes at most 8", len(m.Island.Scenery))
	}
	for i, s := range m.Island.Scenery {
		f := fmt.Sprintf("island.scenery[%d]", i)
		p.model(f+".model", s.Model)
		p.number(f+".perRadius", s.PerRadius, 0, 4)
		p.number(f+".pad", s.Pad, 0, 4)
		p.number(f+".scale[0]", s.Scale[0], 0.1, 4)
		p.number(f+".scale[1]", s.Scale[1], 0.1, 4)
		if s.Scale[1] < s.Scale[0] {
			p.add("%s.scale goes from %v down to %v", f, s.Scale[0], s.Scale[1])
		}
	}
	p.model("ring.model", m.Ring.Model)
	p.number("ring.spacing", m.Ring.Spacing, 0.2, 10)
	p.number("ring.spin", m.Ring.Spin, -3, 3)
	p.model("terrace.plaza", m.Terrace.Plaza)
	p.model("terrace.post", m.Terrace.Post)
	p.number("terrace.postSpacing", m.Terrace.PostSpacing, 0.3, 10)
	p.number("terrace.postOut", m.Terrace.PostOut, 0, 2)
	p.number("terrace.glow", m.Terrace.Glow, 0, 2)
	if r := m.Terrace.Rope; r != nil {
		if len(r.At) != 3 {
			p.add("terrace.rope.at has %d numbers; it is [x, y, z]", len(r.At))
		} else {
			p.number("terrace.rope.at[0]", r.At[0], -2, 2)
			p.number("terrace.rope.at[1]", r.At[1], 0, 4)
			p.number("terrace.rope.at[2]", r.At[2], -2, 2)
		}
		p.number("terrace.rope.sag", r.Sag, 0, 2)
		p.number("terrace.rope.radius", r.Radius, 0.002, 0.2)
		p.model("terrace.rope.material", r.Material)
		if b := r.Bead; b != nil {
			p.model("terrace.rope.bead.model", b.Model)
			p.number("terrace.rope.bead.spacing", b.Spacing, 0.1, 10)
		}
	}
	p.model("building.model", m.Building.Model)
	p.model("airship.model", m.Airship.Model)
	for prov, name := range m.Airship.Providers {
		if !slices.Contains(Providers, prov) {
			p.add("airship.providers names %q, which isn't one of %s", prov, strings.Join(Providers, ", "))
		}
		p.model("airship.providers."+prov, name)
	}
	if v := m.Visitor; v != nil {
		p.model("visitor.model", v.Model)
		if !slices.Contains(VisitorPaths, v.Path) {
			p.add("visitor.path is %q, which isn't one of %s", v.Path, strings.Join(VisitorPaths, ", "))
		}
		p.number("visitor.duration", v.Duration, 2, 30)
	}
}

// models is every model the manifest names, with the parts each needs,
// for checking against models.glb.
func (m *Manifest) models() []modelUse {
	uses := []modelUse{
		{"citadel.model", m.Citadel.Model, []string{"core", "halo", "beacon"}, false},
		{"ring.model", m.Ring.Model, nil, false},
		{"terrace.plaza", m.Terrace.Plaza, nil, false},
		{"terrace.post", m.Terrace.Post, nil, false},
		{"building.model", m.Building.Model, nil, true},
		{"airship.model", m.Airship.Model, nil, false},
	}
	for i, name := range m.Island.Models {
		uses = append(uses, modelUse{fmt.Sprintf("island.models[%d]", i), name, []string{"core"}, false})
	}
	for i, s := range m.Island.Scenery {
		uses = append(uses, modelUse{fmt.Sprintf("island.scenery[%d].model", i), s.Model, nil, false})
	}
	for _, prov := range Providers {
		if name, ok := m.Airship.Providers[prov]; ok {
			uses = append(uses, modelUse{"airship.providers." + prov, name, nil, false})
		}
	}
	if m.Visitor != nil {
		uses = append(uses, modelUse{"visitor.model", m.Visitor.Model, nil, false})
	}
	if r := m.Terrace.Rope; r != nil && r.Bead != nil {
		uses = append(uses, modelUse{"terrace.rope.bead.model", r.Bead.Model, nil, false})
	}
	return uses
}

// modelUse is one place the manifest names a model: the parts it must
// have, and whether its root must be one mesh of one primitive, as a
// building's body is.
type modelUse struct {
	field    string
	name     string
	parts    []string
	soleMesh bool
}
