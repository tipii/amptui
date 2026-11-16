// Command notices writes the licenses of the third-party Go modules
// compiled into amptui, for release archives and packages to ship next
// to the binary (MIT, BSD and MPL all ask for their notice to travel
// with binaries).
//
//	go run ./scripts/notices > THIRD_PARTY_NOTICES
//
// It fails when a module has no license file, so a new dependency can't
// slip into a release unnoticed.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// licenseFile matches the files a module's license and notices live in.
var licenseFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice)(\.(md|txt))?$`)

type module struct{ path, version, dir string }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "notices:", err)
		os.Exit(1)
	}
}

func run() error {
	mods, err := dependencies("./cmd/amptui", "linux", "darwin")
	if err != nil {
		return err
	}
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	fmt.Fprintf(out, "amptui includes the following %d third-party Go modules.\nTheir licenses follow.\n", len(mods))
	rule := strings.Repeat("=", 72)
	for _, m := range mods {
		files, err := licenseFiles(m.dir)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return fmt.Errorf("%s %s has no license file in %s", m.path, m.version, m.dir)
		}
		fmt.Fprintf(out, "\n%s\n%s %s\n%s\n", rule, m.path, m.version, rule)
		for _, f := range files {
			text, err := os.ReadFile(filepath.Join(m.dir, f))
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "\n%s\n", bytes.TrimSpace(text))
		}
	}
	return nil
}

// dependencies lists the modules providing the packages pkg compiles in
// on any of the release's operating systems, other than the main module,
// sorted by path.
func dependencies(pkg string, goos ...string) ([]module, error) {
	seen := map[string]bool{}
	var mods []module
	for _, target := range goos {
		cmd := exec.Command("go", "list", "-deps",
			"-f", `{{with .Module}}{{if not .Main}}{{.Path}}	{{.Version}}	{{.Dir}}{{end}}{{end}}`, pkg)
		cmd.Env = append(os.Environ(), "GOOS="+target)
		cmd.Stderr = os.Stderr
		raw, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list (GOOS=%s): %w", target, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			f := strings.Split(line, "\t")
			if len(f) != 3 || seen[f[0]] {
				continue
			}
			seen[f[0]] = true
			mods = append(mods, module{path: f[0], version: f[1], dir: f[2]})
		}
	}
	slices.SortFunc(mods, func(a, b module) int { return strings.Compare(a.path, b.path) })
	return mods, nil
}

// licenseFiles are the license / notice files at the root of dir.
func licenseFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && licenseFile.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
