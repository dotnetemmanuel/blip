// Package detect works out what API framework, if any, a project directory
// runs, from the manifests it finds on disk.
package detect

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/dotnetemmanuel/blip/internal/output"
)

// Framework is one API-serving project found under a repo root.
type Framework struct {
	Name         string
	Manifest     string
	SpecPaths    []string
	DefaultPort  int
	StartCommand []string
	StartEnv     map[string]string
}

// skipDirs never hold a project of their own, only build output or a copy of
// another project's manifests.
var skipDirs = map[string]bool{
	"node_modules": true,
	"bin":          true,
	"obj":          true,
	"target":       true,
	"vendor":       true,
	".git":         true,
	"dist":         true,
	"build":        true,
	".venv":        true,
	"__pycache__":  true,
}

// registry holds the parts of each Framework that never depend on the
// project it was found in. Name, Manifest and any dynamic StartCommand are
// filled in at match time.
var registry = map[string]Framework{
	"aspnet-openapi": {
		Name:         "aspnet-openapi",
		SpecPaths:    []string{"/openapi/v1.json", "/openapi/v1.yaml"},
		DefaultPort:  5000,
		StartCommand: []string{"dotnet", "run"},
	},
	"aspnet-swashbuckle": {
		Name:         "aspnet-swashbuckle",
		SpecPaths:    []string{"/swagger/v1/swagger.json", "/swagger/v1/swagger.yaml"},
		DefaultPort:  5000,
		StartCommand: []string{"dotnet", "run"},
	},
	"fastapi": {
		Name:        "fastapi",
		SpecPaths:   []string{"/openapi.json"},
		DefaultPort: 8000,
	},
	"nestjs": {
		Name:        "nestjs",
		SpecPaths:   []string{"/api-json", "/api/docs-json", "/openapi.json"},
		DefaultPort: 3000,
	},
	"spring-maven": {
		Name:         "spring-maven",
		SpecPaths:    []string{"/v3/api-docs"},
		DefaultPort:  8080,
		StartCommand: []string{"./mvnw", "spring-boot:run"},
	},
	"spring-gradle": {
		Name:         "spring-gradle",
		SpecPaths:    []string{"/v3/api-docs"},
		DefaultPort:  8080,
		StartCommand: []string{"./gradlew", "bootRun"},
	},
	"go-swaggo": {
		Name:         "go-swaggo",
		SpecPaths:    []string{"/swagger/doc.json"},
		DefaultPort:  8080,
		StartCommand: []string{"go", "run", "."},
	},
	"rust-utoipa": {
		Name:         "rust-utoipa",
		SpecPaths:    []string{"/api-docs/openapi.json"},
		DefaultPort:  8080,
		StartCommand: []string{"cargo", "run"},
	},
}

// DetectFrameworks walks repoRoot and returns one Framework per project
// directory found. A manifest name is never enough on its own: each match is
// confirmed by reading the file for the dependency that actually serves a
// spec, so a bare package.json or go.mod yields nothing.
func DetectFrameworks(repoRoot string) ([]Framework, error) {
	byDir := map[string]Framework{}

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != repoRoot && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		fw, ok, err := matchManifest(path, d.Name())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		dir := filepath.Dir(path)
		// First match in a directory wins; a directory carrying evidence for
		// two frameworks is resolved inside the manifest-specific matcher instead.
		if _, seen := byDir[dir]; !seen {
			byDir[dir] = fw
		}
		return nil
	})
	if err != nil {
		return nil, output.Configf("scanning %s for frameworks: %w", repoRoot, err)
	}

	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	frameworks := make([]Framework, 0, len(dirs))
	for _, dir := range dirs {
		frameworks = append(frameworks, byDir[dir])
	}
	return frameworks, nil
}

func matchManifest(path, name string) (Framework, bool, error) {
	switch {
	case strings.HasSuffix(name, ".csproj"):
		return matchCsproj(path, name)
	case name == "pyproject.toml":
		return matchPyproject(path, name)
	case name == "requirements.txt":
		return matchRequirementsTxt(path, name)
	case name == "package.json":
		return matchPackageJSON(path, name)
	case name == "pom.xml":
		return matchPomXML(path, name)
	case name == "build.gradle" || name == "build.gradle.kts":
		return matchBuildGradle(path, name)
	case name == "go.mod":
		return matchGoMod(path, name)
	case name == "Cargo.toml":
		return matchCargoToml(path, name)
	}
	return Framework{}, false, nil
}

func withManifest(fw Framework, manifest string) Framework {
	fw.Manifest = manifest
	return fw
}

func matchCsproj(path, name string) (Framework, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Framework{}, false, err
	}
	content := string(data)
	switch {
	// Checked first: a .csproj naming both packages is mid-migration off
	// Swashbuckle, and the built-in generator is the one that will remain.
	case strings.Contains(content, "Microsoft.AspNetCore.OpenApi"):
		return withManifest(registry["aspnet-openapi"], name), true, nil
	case strings.Contains(content, "Swashbuckle"):
		return withManifest(registry["aspnet-swashbuckle"], name), true, nil
	}
	return Framework{}, false, nil
}

var depNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+`)

func dependsOn(deps []string, pkg string) bool {
	for _, d := range deps {
		if strings.EqualFold(depNameRe.FindString(strings.TrimSpace(d)), pkg) {
			return true
		}
	}
	return false
}

func matchPyproject(path, name string) (Framework, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Framework{}, false, err
	}
	var doc struct {
		Project struct {
			Dependencies []string `toml:"dependencies"`
		} `toml:"project"`
	}
	// A pyproject.toml that fails to parse is not evidence of anything; skip it
	// rather than failing the whole walk over one malformed manifest.
	if err := toml.Unmarshal(data, &doc); err != nil {
		return Framework{}, false, nil
	}
	if !dependsOn(doc.Project.Dependencies, "fastapi") {
		return Framework{}, false, nil
	}
	fw := withManifest(registry["fastapi"], name)
	fw.StartCommand = fastapiStartCommand(filepath.Dir(path))
	return fw, true, nil
}

func matchRequirementsTxt(path, name string) (Framework, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Framework{}, false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		pkg := depNameRe.FindString(strings.TrimSpace(line))
		if strings.EqualFold(pkg, "fastapi") {
			fw := withManifest(registry["fastapi"], name)
			fw.StartCommand = fastapiStartCommand(filepath.Dir(path))
			return fw, true, nil
		}
	}
	return Framework{}, false, nil
}

func fastapiStartCommand(dir string) []string {
	for _, stem := range []string{"main", "app"} {
		if _, err := os.Stat(filepath.Join(dir, stem+".py")); err == nil {
			return []string{"uvicorn", stem + ":app"}
		}
	}
	return nil
}

type packageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Scripts         map[string]string `json:"scripts"`
}

func matchPackageJSON(path, name string) (Framework, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Framework{}, false, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return Framework{}, false, nil
	}
	_, direct := pkg.Dependencies["@nestjs/swagger"]
	_, dev := pkg.DevDependencies["@nestjs/swagger"]
	if !direct && !dev {
		return Framework{}, false, nil
	}
	fw := withManifest(registry["nestjs"], name)
	fw.StartCommand = nestStartCommand(pkg.Scripts)
	return fw, true, nil
}

func nestStartCommand(scripts map[string]string) []string {
	if _, ok := scripts["start:dev"]; ok {
		return []string{"npm", "run", "start:dev"}
	}
	return []string{"npm", "start"}
}

func matchPomXML(path, name string) (Framework, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Framework{}, false, err
	}
	if !strings.Contains(string(data), "springdoc") {
		return Framework{}, false, nil
	}
	return withManifest(registry["spring-maven"], name), true, nil
}

func matchBuildGradle(path, name string) (Framework, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Framework{}, false, err
	}
	if !strings.Contains(string(data), "springdoc") {
		return Framework{}, false, nil
	}
	return withManifest(registry["spring-gradle"], name), true, nil
}

func matchGoMod(path, name string) (Framework, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Framework{}, false, err
	}
	if !strings.Contains(string(data), "swaggo") {
		return Framework{}, false, nil
	}
	return withManifest(registry["go-swaggo"], name), true, nil
}

func matchCargoToml(path, name string) (Framework, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Framework{}, false, err
	}
	var doc struct {
		Dependencies map[string]any `toml:"dependencies"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return Framework{}, false, nil
	}
	if _, ok := doc.Dependencies["utoipa"]; !ok {
		return Framework{}, false, nil
	}
	return withManifest(registry["rust-utoipa"], name), true, nil
}
