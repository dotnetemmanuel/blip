package detect

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func testdata(name string) string {
	return filepath.Join("testdata", name)
}

func detectOne(t *testing.T, dir string) Framework {
	t.Helper()
	got, err := DetectFrameworks(testdata(dir))
	if err != nil {
		t.Fatalf("DetectFrameworks(%s): %v", dir, err)
	}
	if len(got) != 1 {
		t.Fatalf("DetectFrameworks(%s) = %d frameworks, want 1: %+v", dir, len(got), got)
	}
	return got[0]
}

func TestDetectFrameworksOneProjectFixtures(t *testing.T) {
	tests := []struct {
		dir          string
		name         string
		specPaths    []string
		defaultPort  int
		startCommand []string
	}{
		{
			dir:          "spring-gradle-kts",
			name:         "spring-gradle",
			specPaths:    []string{"/v3/api-docs"},
			defaultPort:  8080,
			startCommand: []string{"./gradlew", "bootRun"},
		},
		{
			dir:          "aspnet-openapi",
			name:         "aspnet-openapi",
			specPaths:    []string{"/openapi/v1.json", "/openapi/v1.yaml"},
			defaultPort:  5000,
			startCommand: []string{"dotnet", "run"},
		},
		{
			dir:          "aspnet-swashbuckle",
			name:         "aspnet-swashbuckle",
			specPaths:    []string{"/swagger/v1/swagger.json", "/swagger/v1/swagger.yaml"},
			defaultPort:  5000,
			startCommand: []string{"dotnet", "run"},
		},
		{
			dir:         "fastapi-pyproject",
			name:        "fastapi",
			specPaths:   []string{"/openapi.json"},
			defaultPort: 8000,
		},
		{
			dir:         "fastapi-requirements",
			name:        "fastapi",
			specPaths:   []string{"/openapi.json"},
			defaultPort: 8000,
		},
		{
			dir:          "nestjs",
			name:         "nestjs",
			specPaths:    []string{"/api-json", "/api/docs-json", "/openapi.json"},
			defaultPort:  3000,
			startCommand: []string{"npm", "start"},
		},
		{
			dir:          "spring-maven",
			name:         "spring-maven",
			specPaths:    []string{"/v3/api-docs"},
			defaultPort:  8080,
			startCommand: []string{"./mvnw", "spring-boot:run"},
		},
		{
			dir:          "spring-gradle",
			name:         "spring-gradle",
			specPaths:    []string{"/v3/api-docs"},
			defaultPort:  8080,
			startCommand: []string{"./gradlew", "bootRun"},
		},
		{
			dir:          "go-swaggo",
			name:         "go-swaggo",
			specPaths:    []string{"/swagger/doc.json"},
			defaultPort:  8080,
			startCommand: []string{"go", "run", "."},
		},
		{
			dir:          "rust-utoipa",
			name:         "rust-utoipa",
			specPaths:    []string{"/api-docs/openapi.json"},
			defaultPort:  8080,
			startCommand: []string{"cargo", "run"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			fw := detectOne(t, tt.dir)
			if fw.Name != tt.name {
				t.Errorf("Name = %q, want %q", fw.Name, tt.name)
			}
			if !reflect.DeepEqual(fw.SpecPaths, tt.specPaths) {
				t.Errorf("SpecPaths = %v, want %v", fw.SpecPaths, tt.specPaths)
			}
			if fw.DefaultPort != tt.defaultPort {
				t.Errorf("DefaultPort = %d, want %d", fw.DefaultPort, tt.defaultPort)
			}
			if !reflect.DeepEqual(fw.StartCommand, tt.startCommand) {
				t.Errorf("StartCommand = %v, want %v", fw.StartCommand, tt.startCommand)
			}
			if fw.Dir != "." {
				t.Errorf("Dir = %q, want %q (the manifest sits at the fixture root)", fw.Dir, ".")
			}
		})
	}
}

func TestDetectFrameworksNoManifestIsEmptyNotError(t *testing.T) {
	got, err := DetectFrameworks(testdata("empty"))
	if err != nil {
		t.Fatalf("DetectFrameworks: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d frameworks, want 0: %+v", len(got), got)
	}
}

func TestDetectFrameworksManifestAloneIsNotEvidence(t *testing.T) {
	// frontend has a plain package.json (react, vite) with no API dependency.
	// A manifest alone must never be flagged as an API.
	got, err := DetectFrameworks(testdata("frontend"))
	if err != nil {
		t.Fatalf("DetectFrameworks: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d frameworks, want 0: %+v", len(got), got)
	}
}

func TestDetectFrameworksMonorepoReturnsOnePerProject(t *testing.T) {
	got, err := DetectFrameworks(testdata("monorepo"))
	if err != nil {
		t.Fatalf("DetectFrameworks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d frameworks, want 2: %+v", len(got), got)
	}
	dirs := map[string]string{}
	for _, fw := range got {
		dirs[fw.Name] = fw.Dir
	}
	if dirs["aspnet-openapi"] != "orders" || dirs["fastapi"] != "catalog" {
		t.Errorf("got dirs %v, want aspnet-openapi in \"orders\" and fastapi in \"catalog\" (web's plain package.json must not appear)", dirs)
	}
}

func TestDetectFrameworksDoesNotSkipByNameSubstring(t *testing.T) {
	// "distribution" contains the skip word "dist" but is not equal to it, and
	// must still be walked into, not skipped as if it were "dist" itself.
	fw := detectOne(t, "skip-substring-check")
	if fw.Name != "fastapi" {
		t.Errorf("Name = %q, want fastapi", fw.Name)
	}
	if fw.Dir != "distribution" {
		t.Errorf("Dir = %q, want %q", fw.Dir, "distribution")
	}
}

func TestDetectFrameworksSkipsNoiseDirectories(t *testing.T) {
	// aspnet-openapi/obj holds a copy of the csproj, and nestjs/node_modules
	// holds a copy of the package.json. Both must be skipped, or these
	// fixtures would each yield two frameworks instead of one.
	detectOne(t, "aspnet-openapi")
	detectOne(t, "nestjs")
}

func TestDetectFrameworksPrefersOpenApiOverSwashbuckleInOneManifest(t *testing.T) {
	dir := t.TempDir()
	csproj := `<Project Sdk="Microsoft.NET.Sdk.Web">
  <ItemGroup>
    <PackageReference Include="Microsoft.AspNetCore.OpenApi" Version="10.0.9" />
    <PackageReference Include="Swashbuckle.AspNetCore" Version="6.5.0" />
  </ItemGroup>
</Project>
`
	if err := os.WriteFile(filepath.Join(dir, "Api.csproj"), []byte(csproj), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := DetectFrameworks(dir)
	if err != nil {
		t.Fatalf("DetectFrameworks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d frameworks, want 1: %+v", len(got), got)
	}
	if got[0].Name != "aspnet-openapi" {
		t.Errorf("Name = %q, want aspnet-openapi (mid-migration project should prefer the built-in generator)", got[0].Name)
	}
}

func detectOneIn(t *testing.T, dir string) Framework {
	t.Helper()
	got, err := DetectFrameworks(dir)
	if err != nil {
		t.Fatalf("DetectFrameworks(%s): %v", dir, err)
	}
	if len(got) != 1 {
		t.Fatalf("DetectFrameworks(%s) = %d frameworks, want 1: %+v", dir, len(got), got)
	}
	return got[0]
}

func TestDetectFrameworksFastAPIStartCommandNeedsAnEntrypointBesideTheManifest(t *testing.T) {
	pyproject := `[project]
name = "catalog"
dependencies = ["fastapi>=0.115"]
`
	t.Run("no entrypoint file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(pyproject), 0o644); err != nil {
			t.Fatal(err)
		}
		fw := detectOneIn(t, dir)
		if fw.StartCommand != nil {
			t.Errorf("StartCommand = %v, want nil", fw.StartCommand)
		}
	})

	t.Run("main.py beside the manifest", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(pyproject), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		fw := detectOneIn(t, dir)
		want := []string{"uvicorn", "main:app"}
		if !reflect.DeepEqual(fw.StartCommand, want) {
			t.Errorf("StartCommand = %v, want %v", fw.StartCommand, want)
		}
	})

	t.Run("app.py beside the manifest", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(pyproject), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		fw := detectOneIn(t, dir)
		want := []string{"uvicorn", "app:app"}
		if !reflect.DeepEqual(fw.StartCommand, want) {
			t.Errorf("StartCommand = %v, want %v", fw.StartCommand, want)
		}
	})
}

func TestDetectFrameworksNestJSPrefersStartDevScript(t *testing.T) {
	dir := t.TempDir()
	pkg := `{
  "name": "orders",
  "dependencies": { "@nestjs/core": "^10.0.0", "@nestjs/swagger": "^7.4.0" },
  "scripts": { "start": "nest start", "start:dev": "nest start --watch" }
}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := DetectFrameworks(dir)
	if err != nil {
		t.Fatalf("DetectFrameworks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d frameworks, want 1: %+v", len(got), got)
	}
	want := []string{"npm", "run", "start:dev"}
	if !reflect.DeepEqual(got[0].StartCommand, want) {
		t.Errorf("StartCommand = %v, want %v", got[0].StartCommand, want)
	}
}
