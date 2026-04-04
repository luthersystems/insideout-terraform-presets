package compose

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	terraformpresets "github.com/luthersystems/insideout-terraform-presets"
)

var allowedExt = map[string]bool{
	".tf": true, ".tfvars": true, ".tf.json": true,
	".terraform-version": true, ".tmpl": true, ".zip": true,
}

// DefaultFS returns the embedded filesystem containing all preset modules.
func DefaultFS() fs.FS {
	return terraformpresets.FS
}

// GetPresetFiles returns a map of "/<relpath>" → file bytes for a preset path.
// The fsys parameter is the filesystem to read from (use DefaultFS() for the
// embedded presets).
func GetPresetFiles(fsys fs.FS, presetPath string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(fsys, presetPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !allowedExt[ext] {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, presetPath)
		rel = strings.TrimPrefix(rel, "/")
		out["/"+rel] = b
		return nil
	})
	return out, err
}

// ListPresets lists all available preset paths (e.g., "aws/vpc", "gcp/gke").
func ListPresets(fsys fs.FS) ([]string, error) {
	clouds, err := listDirs(fsys, ".")
	if err != nil {
		return nil, err
	}
	var presets []string
	for _, cloud := range clouds {
		modules, err := listDirs(fsys, cloud)
		if err != nil {
			continue
		}
		for _, mod := range modules {
			presets = append(presets, cloud+"/"+mod)
		}
	}
	sort.Strings(presets)
	return presets, nil
}

// ListPresetsForCloud lists preset paths for a specific cloud (e.g., "aws").
func ListPresetsForCloud(fsys fs.FS, cloud string) ([]string, error) {
	modules, err := listDirs(fsys, cloud)
	if err != nil {
		return nil, err
	}
	var presets []string
	for _, mod := range modules {
		presets = append(presets, cloud+"/"+mod)
	}
	sort.Strings(presets)
	return presets, nil
}

func listDirs(fsys fs.FS, path string) ([]string, error) {
	ents, err := fs.ReadDir(fsys, path)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range ents {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs, nil
}
