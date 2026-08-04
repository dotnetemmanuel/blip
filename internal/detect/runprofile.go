package detect

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// RunProfile is the port and environment a project actually uses when run locally, not its framework's generic default.
type RunProfile struct {
	BaseURL string
	Env     map[string]string
}

// ReadRunProfile reads fw's run configuration; BaseURL is empty when the port is unknown, and ok is false only when nothing was found at all.
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

// asRunProfile applies the shared contract: an empty result is no profile, anything else is one.
func asRunProfile(baseURL string, env map[string]string) (RunProfile, bool) {
	if baseURL == "" && len(env) == 0 {
		return RunProfile{}, false
	}
	return RunProfile{BaseURL: baseURL, Env: env}, true
}

func readLaunchSettings(dir string) (RunProfile, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "Properties", "launchSettings.json"))
	if err != nil {
		return RunProfile{}, false
	}
	applicationURL, env, ok := firstProjectProfile(data)
	if !ok {
		return RunProfile{}, false
	}
	baseURL := ""
	if applicationURL != "" {
		baseURL = preferHTTPS(applicationURL)
	}
	return asRunProfile(baseURL, env)
}

type launchProfile struct {
	CommandName          string            `json:"commandName"`
	ApplicationURL       string            `json:"applicationUrl"`
	EnvironmentVariables map[string]string `json:"environmentVariables"`
}

// firstProjectProfile walks the raw JSON tokens in file order to find the first commandName Project profile, since a map would lose that order.
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
	baseURL := ""
	if port, ok := env["PORT"]; ok {
		if _, err := strconv.Atoi(port); err == nil {
			baseURL = "http://localhost:" + port
		}
	}
	return asRunProfile(baseURL, env)
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

// readSpringConfig prefers a candidate declaring server.port, else surfaces the first candidate's other properties.
func readSpringConfig(dir string) (RunProfile, bool) {
	var fallback map[string]string
	for _, rel := range springConfigPaths {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		env, ok := springProperties(rel, data)
		if !ok {
			continue
		}
		if port, ok := env["server.port"]; ok {
			if _, err := strconv.Atoi(port); err == nil {
				return RunProfile{BaseURL: "http://localhost:" + port, Env: env}, true
			}
		}
		if fallback == nil && len(env) > 0 {
			fallback = env
		}
	}
	return asRunProfile("", fallback)
}

func springProperties(name string, data []byte) (map[string]string, bool) {
	if strings.HasSuffix(name, ".properties") {
		return parsePropertiesFile(data), true
	}
	return parseSpringYAML(data)
}

func parsePropertiesFile(data []byte) map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		env[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return env
}

func parseSpringYAML(data []byte) (map[string]string, bool) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	env := map[string]string{}
	flattenYAML("", doc, env)
	return env, true
}

// flattenYAML turns nested YAML into Spring's own dotted-key property form, e.g. server.port.
func flattenYAML(prefix string, node map[string]any, out map[string]string) {
	for k, v := range node {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if nested, ok := v.(map[string]any); ok {
			flattenYAML(key, nested, out)
			continue
		}
		out[key] = fmt.Sprint(v)
	}
}
