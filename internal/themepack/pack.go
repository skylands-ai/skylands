// Package themepack reads, checks, and stores theme packs: looks for the
// 3D world made of data alone, with no code. A pack is a manifest,
// theme.json, saying which of the models in one glTF binary, models.glb,
// the world draws each thing with, and a LICENSE for them.
//
// A pack never runs in the page. The world places every model, tags what
// a click on it means, and plays only the motions this format offers, so
// a pack changes how the world is drawn and nothing else. Check refuses
// anything in models.glb that could reach outside it, such as a URI or a
// glTF extension, and anything over the limits that keep a pack light.
package themepack

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The files a pack is made of, in the byte order its hash lists them in.
const (
	LicenseFile  = "LICENSE"
	ModelsFile   = "models.glb"
	ManifestFile = "theme.json"
)

// Files is every file in a pack, in the order its hash lists them.
var Files = []string{LicenseFile, ModelsFile, ManifestFile}

// Size limits for each file in a pack.
var maxSize = map[string]int{
	LicenseFile:  64 << 10,
	ModelsFile:   4 << 20,
	ManifestFile: 64 << 10,
}

// MaxArchive is the most a zipped pack may be, before or after unzipping.
const MaxArchive = 5 << 20

// Pack is a checked theme pack.
type Pack struct {
	Manifest *Manifest
	// Files holds every file in the pack by name.
	Files map[string][]byte
	// Hash pins the pack's contents: the SHA-256, in hex, of what
	// sha256sum prints for its files in the order of Files, so
	// "sha256sum LICENSE models.glb theme.json | sha256sum" in a pack's
	// directory gives the same.
	Hash string
	// Triangles is how many triangles models.glb holds.
	Triangles int
}

// InvalidError lists everything wrong with a pack.
type InvalidError struct {
	Problems []string
}

func (e *InvalidError) Error() string {
	if len(e.Problems) == 1 {
		return "not a valid theme pack: " + e.Problems[0]
	}
	return "not a valid theme pack:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// Check checks files as a pack and returns it, or an *InvalidError
// listing everything wrong with it.
func Check(files map[string][]byte) (*Pack, error) {
	var p problems
	for _, name := range Files {
		data, ok := files[name]
		switch {
		case !ok:
			p.add("the pack has no %s", name)
		case len(data) > maxSize[name]:
			p.add("%s is %d bytes; it may be at most %d", name, len(data), maxSize[name])
		}
	}
	for name := range files {
		if !slices.Contains(Files, name) {
			p.add("the pack has %s, which isn't one of %s", name, strings.Join(Files, ", "))
		}
	}
	if len(p) > 0 {
		return nil, &InvalidError{p}
	}
	m, err := ParseManifest(files[ManifestFile])
	if err != nil {
		return nil, &InvalidError{[]string{err.Error()}}
	}
	m.check(&p)
	ms := checkGLB(files[ModelsFile], &p)
	if ms != nil && len(p) == 0 {
		checkModels(m, ms, &p)
	}
	if len(p) > 0 {
		return nil, &InvalidError{p}
	}
	return &Pack{Manifest: m, Files: files, Hash: Hash(files), Triangles: ms.triangles}, nil
}

// Hash is the hash that pins a pack with these files.
func Hash(files map[string][]byte) string {
	var list strings.Builder
	for _, name := range Files {
		sum := sha256.Sum256(files[name])
		fmt.Fprintf(&list, "%x  %s\n", sum, name)
	}
	sum := sha256.Sum256([]byte(list.String()))
	return hex.EncodeToString(sum[:])
}

// ReadDir reads a pack's files from a directory, and names the other
// files there, which aren't part of the pack.
func ReadDir(dir string) (files map[string][]byte, others []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	files = map[string][]byte{}
	for _, e := range entries {
		name := e.Name()
		if !slices.Contains(Files, name) {
			others = append(others, name)
			continue
		}
		if !e.Type().IsRegular() {
			return nil, nil, fmt.Errorf("%s isn't a regular file", filepath.Join(dir, name))
		}
		data, err := readLimited(filepath.Join(dir, name), maxSize[name])
		if err != nil {
			return nil, nil, err
		}
		files[name] = data
	}
	return files, others, nil
}

func readLimited(name string, limit int) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%s is over %d bytes", name, limit)
	}
	return data, nil
}

// ReadZip reads a pack's files from a zip archive, at its root or in one
// directory that holds everything, as a zipped pack directory does, and
// names the other files there.
func ReadZip(data []byte) (files map[string][]byte, others []string, err error) {
	if len(data) > MaxArchive {
		return nil, nil, fmt.Errorf("the archive is %d bytes; a pack may be at most %d", len(data), MaxArchive)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, fmt.Errorf("read the archive: %w", err)
	}
	prefix := commonDir(zr.File)
	files = map[string][]byte{}
	total := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.TrimPrefix(f.Name, prefix)
		if !slices.Contains(Files, name) {
			others = append(others, name)
			continue
		}
		if _, dup := files[name]; dup {
			return nil, nil, fmt.Errorf("the archive has %s twice", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, fmt.Errorf("read %s from the archive: %w", name, err)
		}
		b, err := io.ReadAll(io.LimitReader(rc, int64(maxSize[name])+1))
		_ = rc.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("read %s from the archive: %w", name, err)
		}
		if len(b) > maxSize[name] {
			return nil, nil, fmt.Errorf("%s is over %d bytes", name, maxSize[name])
		}
		if total += len(b); total > MaxArchive {
			return nil, nil, fmt.Errorf("the archive unzips to over %d bytes", MaxArchive)
		}
		files[name] = b
	}
	return files, others, nil
}

// commonDir is the one directory every file in an archive is in, with its
// slash, or "" when they aren't all in one.
func commonDir(fs []*zip.File) string {
	dir := ""
	for i, f := range fs {
		first, _, ok := strings.Cut(f.Name, "/")
		if !ok {
			return ""
		}
		if i == 0 {
			dir = first
		} else if first != dir {
			return ""
		}
	}
	if dir == "" {
		return ""
	}
	return dir + "/"
}

// Archive zips the pack's files, the same bytes for the same pack.
func (p *Pack) Archive() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range Files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(p.Files[name]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Read reads and checks the pack at path, a directory or a zip archive,
// and names the files there that aren't part of it.
func Read(name string) (*Pack, []string, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, nil, err
	}
	var files map[string][]byte
	var others []string
	if info.IsDir() {
		files, others, err = ReadDir(name)
	} else {
		var data []byte
		data, err = readLimited(name, MaxArchive)
		if err == nil {
			files, others, err = ReadZip(data)
		}
	}
	if err != nil {
		return nil, nil, err
	}
	p, err := Check(files)
	return p, others, err
}

// IsInvalid says whether err says a pack is invalid, rather than that it
// couldn't be read.
func IsInvalid(err error) bool {
	var inv *InvalidError
	return errors.As(err, &inv)
}

// Served is the path a citadel serves one of a pack's files at. The hash
// is in it, so a browser can keep the file for good.
func Served(id, hash, file string) string {
	return path.Join("/themes", id, hash, file)
}
