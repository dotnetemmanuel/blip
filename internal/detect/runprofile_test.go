package detect

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadRunProfileAspNetOpenAPIRealLaunchSettings(t *testing.T) {
	fw := detectOne(t, "aspnet-openapi")
	got, ok := ReadRunProfile(testdata("aspnet-openapi"), fw)
	if !ok {
		t.Fatalf("ReadRunProfile: ok = false, want true")
	}
	if got.BaseURL != "https://localhost:44397" {
		t.Errorf("BaseURL = %q, want %q", got.BaseURL, "https://localhost:44397")
	}
	if got.Env["ASPNETCORE_ENVIRONMENT"] != "Development" {
		t.Errorf("Env[ASPNETCORE_ENVIRONMENT] = %q, want %q", got.Env["ASPNETCORE_ENVIRONMENT"], "Development")
	}
}

func TestReadRunProfileAspNetSkipsNonProjectProfileAndPrefersHTTPS(t *testing.T) {
	// Two non-Project profiles precede the Project one, so a map-based regression that drops file order fails reliably rather than half the time.
	fw := detectOne(t, "aspnet-swashbuckle")
	got, ok := ReadRunProfile(testdata("aspnet-swashbuckle"), fw)
	if !ok {
		t.Fatalf("ReadRunProfile: ok = false, want true")
	}
	if got.BaseURL != "https://localhost:7171" {
		t.Errorf("BaseURL = %q, want %q (https must win over the IIS Express profile and the http URL)", got.BaseURL, "https://localhost:7171")
	}
	if len(got.Env) != 0 {
		t.Errorf("Env = %v, want empty (the Project profile declares no environmentVariables)", got.Env)
	}
}

func TestReadRunProfileAspNetMalformedFileIsNoProfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Properties"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Properties", "launchSettings.json"), []byte("{ not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	fw := Framework{Name: "aspnet-openapi", Dir: "."}
	_, ok := ReadRunProfile(dir, fw)
	if ok {
		t.Errorf("ReadRunProfile: ok = true, want false for a malformed launchSettings.json")
	}
}

func TestReadRunProfileAspNetNoProfileFileFallsBack(t *testing.T) {
	// monorepo/orders is a real aspnet-openapi fixture with no Properties directory at all.
	got, err := DetectFrameworks(testdata("monorepo"))
	if err != nil {
		t.Fatalf("DetectFrameworks: %v", err)
	}
	var orders Framework
	for _, fw := range got {
		if fw.Dir == "orders" {
			orders = fw
		}
	}
	if orders.Name != "aspnet-openapi" {
		t.Fatalf("could not find the orders framework in %+v", got)
	}
	_, ok := ReadRunProfile(testdata("monorepo"), orders)
	if ok {
		t.Errorf("ReadRunProfile: ok = true, want false (no launchSettings.json exists)")
	}
}

func TestReadRunProfileAspNetNoProjectProfileAtAllIsNoProfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Properties"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{
  "profiles": {
    "IIS Express": {
      "commandName": "IISExpress",
      "applicationUrl": "http://localhost:5171"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "Properties", "launchSettings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	fw := Framework{Name: "aspnet-openapi", Dir: "."}
	_, ok := ReadRunProfile(dir, fw)
	if ok {
		t.Errorf("ReadRunProfile: ok = true, want false (no profile has commandName Project)")
	}
}

func TestReadRunProfileAspNetEnvWithoutURLStillReturnsProfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Properties"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{
  "profiles": {
    "Api": {
      "commandName": "Project",
      "environmentVariables": {
        "ASPNETCORE_ENVIRONMENT": "Development"
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "Properties", "launchSettings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	fw := Framework{Name: "aspnet-openapi", Dir: "."}
	got, ok := ReadRunProfile(dir, fw)
	if !ok {
		t.Fatalf("ReadRunProfile: ok = false, want true (environmentVariables were found, only applicationUrl is missing)")
	}
	if got.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty (caller should fall back to DefaultPort)", got.BaseURL)
	}
	if got.Env["ASPNETCORE_ENVIRONMENT"] != "Development" {
		t.Errorf("Env[ASPNETCORE_ENVIRONMENT] = %q, want %q", got.Env["ASPNETCORE_ENVIRONMENT"], "Development")
	}
}

func TestReadRunProfileNode(t *testing.T) {
	fw := detectOne(t, "nestjs")
	got, ok := ReadRunProfile(testdata("nestjs"), fw)
	if !ok {
		t.Fatalf("ReadRunProfile: ok = false, want true")
	}
	if got.BaseURL != "http://localhost:3005" {
		t.Errorf("BaseURL = %q, want %q", got.BaseURL, "http://localhost:3005")
	}
	want := map[string]string{"PORT": "3005", "NODE_ENV": "development"}
	if !reflect.DeepEqual(got.Env, want) {
		t.Errorf("Env = %v, want %v", got.Env, want)
	}
}

func TestReadRunProfileNodeNoEnvFileIsNoProfile(t *testing.T) {
	dir := t.TempDir()
	pkg := `{"name": "orders", "dependencies": {"@nestjs/swagger": "^7.4.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	fw := Framework{Name: "nestjs", Dir: "."}
	_, ok := ReadRunProfile(dir, fw)
	if ok {
		t.Errorf("ReadRunProfile: ok = true, want false (no .env file)")
	}
}

func TestReadRunProfileNodeMissingPortStillReturnsEnv(t *testing.T) {
	// process.env.PORT || 3000 in app code means .env can lack PORT, but the secrets in it still matter.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("NODE_ENV=development\nDATABASE_URL=postgres://localhost/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fw := Framework{Name: "nestjs", Dir: "."}
	got, ok := ReadRunProfile(dir, fw)
	if !ok {
		t.Fatalf("ReadRunProfile: ok = false, want true (.env has real content, just no PORT)")
	}
	if got.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty (caller should fall back to DefaultPort)", got.BaseURL)
	}
	want := map[string]string{"NODE_ENV": "development", "DATABASE_URL": "postgres://localhost/app"}
	if !reflect.DeepEqual(got.Env, want) {
		t.Errorf("Env = %v, want %v", got.Env, want)
	}
}

func TestReadRunProfileNodeMalformedPortStillReturnsEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("PORT=not-a-number\nNODE_ENV=development\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fw := Framework{Name: "nestjs", Dir: "."}
	got, ok := ReadRunProfile(dir, fw)
	if !ok {
		t.Fatalf("ReadRunProfile: ok = false, want true (NODE_ENV was still read)")
	}
	if got.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty (PORT is not numeric)", got.BaseURL)
	}
	if got.Env["NODE_ENV"] != "development" {
		t.Errorf("Env[NODE_ENV] = %q, want %q", got.Env["NODE_ENV"], "development")
	}
}

func TestReadRunProfileNodeEmptyEnvFileIsNoProfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("# nothing to see here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fw := Framework{Name: "nestjs", Dir: "."}
	_, ok := ReadRunProfile(dir, fw)
	if ok {
		t.Errorf("ReadRunProfile: ok = true, want false (.env has no usable content)")
	}
}

func TestReadRunProfileSpring(t *testing.T) {
	tests := []struct {
		dir  string
		name string
		want string
	}{
		{"spring-maven", "spring-maven", "http://localhost:8081"},       // src/main/resources/application.yml
		{"spring-gradle-kts", "spring-gradle", "http://localhost:8082"}, // root application.yaml
		{"spring-gradle", "spring-gradle", "http://localhost:8083"},     // root application.properties
	}
	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			fw := detectOne(t, tt.dir)
			got, ok := ReadRunProfile(testdata(tt.dir), fw)
			if !ok {
				t.Fatalf("ReadRunProfile: ok = false, want true")
			}
			if got.BaseURL != tt.want {
				t.Errorf("BaseURL = %q, want %q", got.BaseURL, tt.want)
			}
		})
	}
}

func TestReadRunProfileSpringPropertiesWithoutPortStillReturnsEnv(t *testing.T) {
	dir := t.TempDir()
	props := "spring.application.name=catalog\nspring.datasource.url=jdbc:postgresql://localhost/catalog\n"
	if err := os.WriteFile(filepath.Join(dir, "application.properties"), []byte(props), 0o644); err != nil {
		t.Fatal(err)
	}
	fw := Framework{Name: "spring-maven", Dir: "."}
	got, ok := ReadRunProfile(dir, fw)
	if !ok {
		t.Fatalf("ReadRunProfile: ok = false, want true (properties were found, only server.port is missing)")
	}
	if got.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty (caller should fall back to DefaultPort)", got.BaseURL)
	}
	if got.Env["spring.application.name"] != "catalog" {
		t.Errorf("Env[spring.application.name] = %q, want %q", got.Env["spring.application.name"], "catalog")
	}
}

func TestReadRunProfileSpringMalformedYAMLIsNoProfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "application.yml"), []byte("server: [unterminated"), 0o644); err != nil {
		t.Fatal(err)
	}
	fw := Framework{Name: "spring-maven", Dir: "."}
	_, ok := ReadRunProfile(dir, fw)
	if ok {
		t.Errorf("ReadRunProfile: ok = true, want false (malformed application.yml)")
	}
}

func TestReadRunProfileSpringNoConfigFileIsNoProfile(t *testing.T) {
	dir := t.TempDir()
	fw := Framework{Name: "spring-gradle", Dir: "."}
	_, ok := ReadRunProfile(dir, fw)
	if ok {
		t.Errorf("ReadRunProfile: ok = true, want false (no application config file)")
	}
}

func TestReadRunProfileUnsupportedFrameworkIsNoProfile(t *testing.T) {
	// go-swaggo and rust-utoipa have no run profile reader, so both fall back to DefaultPort.
	for _, dir := range []string{"go-swaggo", "rust-utoipa"} {
		t.Run(dir, func(t *testing.T) {
			fw := detectOne(t, dir)
			_, ok := ReadRunProfile(testdata(dir), fw)
			if ok {
				t.Errorf("ReadRunProfile: ok = true, want false (no reader exists for %s)", fw.Name)
			}
		})
	}
}
