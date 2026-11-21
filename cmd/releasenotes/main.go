// Command releasenotes prints the notes of a presets release: the merged PRs
// since the previous release tag, and a Module Changes section that lists,
// for each changed module, its added and removed variables and outputs and
// its changed defaults, and marks the changes that can break a caller.
//
// Why: the InsideOut backend reads Module Changes when it bumps this module,
// and PR titles alone do not show a removed variable. The release skill used
// to add the section by hand with `gh release edit` after the release; agent
// sessions cannot edit a release, so v0.15.3 shipped without it. The shared
// release workflow runs this as its notes_command, so the release is created
// with the section and nobody edits it after.
//
//	LAST_TAG=v0.15.2 go run ./cmd/releasenotes
//
// LAST_TAG is set by the shared workflow (empty for a first release). The
// notes cover LAST_TAG..HEAD. GITHUB_REPOSITORY and GITHUB_SHA, when set,
// add the compare link.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	out, err := notes(os.Getenv("LAST_TAG"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "releasenotes:", err)
		os.Exit(1)
	}
	fmt.Print(out)
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
	rangeArg := "HEAD"
	if lastTag != "" {
		rangeArg = lastTag + "..HEAD"
	}
	// One bullet per change on main: a merge commit is one PR. No cap: a
	// cut list would hide PRs while Module Changes covers the whole diff.
	shas, err := git("rev-list", "--first-parent", rangeArg)
	if err != nil {
		return "", err
	}
	var prs []string
	for _, sha := range lines(shas) {
		subject, err := git("log", "-1", "--format=%s", sha)
		if err != nil {
			return "", err
		}
		body, err := git("log", "-1", "--format=%b", sha)
		if err != nil {
			return "", err
		}
		prs = append(prs, prLine(sha, strings.TrimSpace(subject), body))
	}

	var changed []string
	if lastTag != "" {
		d, err := git("diff", "--name-only", lastTag, "HEAD")
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

	compare := ""
	if repo, sha := os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_SHA"); repo != "" && sha != "" && lastTag != "" {
		compare = fmt.Sprintf("https://github.com/%s/compare/%s...%s", repo, lastTag, sha)
	}
	return render(prs, changes, changedPackages(changed), compare), nil
}
