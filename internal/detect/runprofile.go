package detect

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// RunProfile is the port and environment a project actually uses when run locally,
// as opposed to its framework's generic default.
type RunProfile struct {
	BaseURL string
	Env     map[string]string
}

// ReadRunProfile reads fw's local run configuration from under repoRoot. It reports
// false when no profile exists, or it exists but cannot be read or parsed; callers
// fall back to fw.DefaultPort in that case.
func ReadRunProfile(repoRoot string, fw Framework) (RunProfile, bool) {
	dir := filepath.Join(repoRoot, fw.Dir)
	switch fw.Name {
	case "aspnet-openapi", "aspnet-swashbuckle":
		return readLaunchSettings(dir)
	case "nestjs":
		return readNodeEnv(dir)
	case "spring-maven", "spring-gradle":
		return readSpringConfig(dir)
	}
	return RunProfile{}, false
}

func readLaunchSettings(dir string) (RunProfile, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "Properties", "launchSettings.json"))
	if err != nil {
		return RunProfile{}, false
	}
	applicationURL, env, ok := firstProjectProfile(data)
	if !ok || applicationURL == "" {
		return RunProfile{}, false
	}
	return RunProfile{BaseURL: preferHTTPS(applicationURL), Env: env}, true
}

type launchProfile struct {
	CommandName          string            `json:"commandName"`
	ApplicationURL       string            `json:"applicationUrl"`
	EnvironmentVariables map[string]string `json:"environmentVariables"`
}

// firstProjectProfile returns the first profile with commandName "Project", in the
// order the profiles appear in the file. A map would lose that order, so this walks
// the raw JSON token stream instead.
func firstProjectProfile(data []byte) (string, map[string]string, bool) {
	var doc struct {
		Profiles json.RawMessage `json:"profiles"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || len(doc.Profiles) == 0 {
		return "", nil, false
	}

	dec := json.NewDecoder(bytes.NewReader(doc.Profiles))
	if tok, err := dec.Token(); err != nil {
		return "", nil, false
	} else if d, ok := tok.(json.Delim); !ok || d != '{' {
		return "", nil, false
	}

	for dec.More() {
		if _, err := dec.Token(); err != nil { // profile name
			return "", nil, false
		}
		var profile launchProfile
		if err := dec.Decode(&profile); err != nil {
			return "", nil, false
		}
		if profile.CommandName == "Project" {
			return profile.ApplicationURL, profile.EnvironmentVariables, true
		}
	}
	return "", nil, false
}

func preferHTTPS(applicationURL string) string {
	urls := strings.Split(applicationURL, ";")
	for _, u := range urls {
		if strings.HasPrefix(strings.TrimSpace(u), "https://") {
			return strings.TrimSpace(u)
		}
	}
	return strings.TrimSpace(urls[0])
}

func readNodeEnv(dir string) (RunProfile, bool) {
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		return RunProfile{}, false
	}
	env := parseEnvFile(data)
	port, ok := env["PORT"]
	if !ok {
		return RunProfile{}, false
	}
	if _, err := strconv.Atoi(port); err != nil {
		return RunProfile{}, false
	}
	return RunProfile{BaseURL: "http://localhost:" + port, Env: env}, true
}

func parseEnvFile(data []byte) map[string]string {
	env := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		env[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return env
}

var springConfigPaths = []string{
	filepath.Join("src", "main", "resources", "application.yml"),
	filepath.Join("src", "main", "resources", "application.yaml"),
	filepath.Join("src", "main", "resources", "application.properties"),
	"application.yml",
	"application.yaml",
	"application.properties",
}

func readSpringConfig(dir string) (RunProfile, bool) {
	for _, rel := range springConfigPaths {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		port, ok := springPort(rel, data)
		if !ok {
			continue
		}
		return RunProfile{BaseURL: "http://localhost:" + strconv.Itoa(port)}, true
	}
	return RunProfile{}, false
}

func springPort(name string, data []byte) (int, bool) {
	if strings.HasSuffix(name, ".properties") {
		return springPropertiesPort(data)
	}
	return springYAMLPort(data)
}

func springYAMLPort(data []byte) (int, bool) {
	var doc struct {
		Server struct {
			Port int `yaml:"port"`
		} `yaml:"server"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil || doc.Server.Port == 0 {
		return 0, false
	}
	return doc.Server.Port, true
}

func springPropertiesPort(data []byte) (int, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || strings.TrimSpace(key) != "server.port" {
			continue
		}
		port, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, false
		}
		return port, true
	}
	return 0, false
}
