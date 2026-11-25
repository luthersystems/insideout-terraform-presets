// Command releasenotes prints the presets' own section of a release's notes,
// Module Changes: for each changed module, its added and removed variables
// and outputs and its changed defaults, with the changes that can break a
// caller marked.
//
// Why: the InsideOut backend reads Module Changes when it bumps this module,
// and PR titles alone do not show a removed variable. The release skill used
// to add the section by hand with `gh release edit` after the release; agent
// sessions cannot edit a release, so v0.15.3 shipped without it. The shared
// release workflow (luthersystems/.github, notes: changes) writes the list of
// changes and the compare link, and runs this as its notes_extra_command, so
// the release is created with the section and nobody edits it after. Only
// this repo can read its HCL, so only the section is here.
//
//	LAST_TAG=v0.15.2 go run ./cmd/releasenotes
//
// LAST_TAG is set by the shared workflow; when it is empty (a first release
// or a major bump) the last v* tag behind HEAD is used, if there is one. The
// section covers LAST_TAG..HEAD; with no module or package change it prints
// nothing.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	out, err := run(os.Getenv("LAST_TAG"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "releasenotes:", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

// run prints the section for a release of HEAD after lastTag.
func run(lastTag string) (string, error) {
	note := ""
	if lastTag == "" {
		// The shared workflow sets LAST_TAG to the latest tag of the new
		// version's major, so a major bump (v0.15.3 to v1.0.0) gets none.
		// Comparing with an empty tree would call every module new and
		// hide the breaking change that made the bump. Compare with the
		// last release tag behind HEAD; with none, this is a first release.
		if t, err := git("describe", "--tags", "--abbrev=0", "--match", "v[0-9]*", "HEAD"); err == nil {
			lastTag = strings.TrimSpace(t)
			note = "Compared with " + lastTag + ", the last release before this one.\n\n"
		}
	}
	out, err := notes(lastTag)
	if err != nil {
		return "", err
	}
	if out != "" && note != "" {
		out = strings.Replace(out, moduleChangesHeading, moduleChangesHeading+note, 1)
	}
	return out, nil
}

func git(args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // G204: fixed git binary, arguments as argv, no shell
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// moduleTree reads, at ref, the module .tf files of the changed modules.
// An empty ref (a first release) is an empty tree.
func moduleTree(ref string, changed []string) (map[string][]byte, error) {
	tree := map[string][]byte{}
	if ref == "" {
		return tree, nil
	}
	dirs := map[string]bool{}
	for _, p := range changed {
		if m := moduleDirRE.FindStringSubmatch(p); m != nil {
			dirs[m[1]] = true
		}
	}
	if len(dirs) == 0 {
		return tree, nil
	}
	args := append([]string{"ls-tree", "--name-only", ref, "--"}, prefixed(sortedKeys(dirs))...)
	ls, err := git(args...)
	if err != nil {
		return nil, err
	}
	for _, p := range lines(ls) {
		if !moduleFileRE.MatchString(p) {
			continue
		}
		src, err := git("show", ref+":"+p)
		if err != nil {
			return nil, err
		}
		tree[p] = []byte(src)
	}
	return tree, nil
}

func prefixed(dirs []string) []string {
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = d + "/"
	}
	return out
}

func notes(lastTag string) (string, error) {
	var changed []string
	if lastTag != "" {
		// --no-renames lists a moved file at its old path too, so a moved
		// or renamed module shows as removed, not only as new.
		d, err := git("diff", "--no-renames", "--name-only", lastTag, "HEAD")
		if err != nil {
			return "", err
		}
		changed = lines(d)
	} else {
		d, err := git("ls-tree", "-r", "--name-only", "HEAD")
		if err != nil {
			return "", err
		}
		changed = lines(d)
	}
	oldTree, err := moduleTree(lastTag, changed)
	if err != nil {
		return "", err
	}
	newTree, err := moduleTree("HEAD", changed)
	if err != nil {
		return "", err
	}
	changes, err := moduleChanges(changed, oldTree, newTree)
	if err != nil {
		return "", err
	}
	return render(changes, changedPackages(changed)), nil
}
