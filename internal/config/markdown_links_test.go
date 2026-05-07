package config

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPublicMarkdownLinksResolve(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	linkRe := regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

	err := walkPublicTextFiles(repoRoot, []string{".md"}, func(path string) error {
		if filepath.Ext(path) != ".md" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range linkRe.FindAllStringSubmatch(string(content), -1) {
			target := strings.TrimSpace(match[1])
			if shouldSkipMarkdownTarget(target) {
				continue
			}
			target = strings.Trim(target, "<>")
			target = strings.SplitN(target, "#", 2)[0]
			target = strings.SplitN(target, "?", 2)[0]
			if target == "" {
				continue
			}
			if decoded, err := url.PathUnescape(target); err == nil {
				target = decoded
			}
			resolved := filepath.Join(filepath.Dir(path), filepath.FromSlash(target))
			if _, err := os.Stat(resolved); err != nil {
				relPath, _ := filepath.Rel(repoRoot, path)
				t.Errorf("%s links to missing local target %q", relPath, match[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk markdown files: %v", err)
	}
}

func TestPublicDocumentationHasNoInternalPlaceholders(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	extensions := []string{".md", ".yml", ".yaml", ".example"}
	forbidden := []string{
		"CON-4",
		"C:\\Users\\kbsar",
		"/Users/kbsar",
		"OWNER/REPOSITORY",
		"owner/repo",
		"AGENTS.md",
		"No license file",
		"Add a LICENSE",
		"TODO",
		"TBD",
	}

	err := walkPublicTextFiles(repoRoot, extensions, func(path string) error {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(content)
		relPath, _ := filepath.Rel(repoRoot, path)
		if relPath == "WORKFLOW.md" {
			return nil
		}
		for _, marker := range forbidden {
			if strings.Contains(text, marker) {
				t.Errorf("%s contains internal or stale public-doc marker %q", relPath, marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk public documentation files: %v", err)
	}
}

func TestPublicJSONExamplesAreValid(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	jsonFenceRe := regexp.MustCompile("(?s)```json\n(.*?)\n```")

	err := walkPublicTextFiles(repoRoot, []string{".md"}, func(path string) error {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(repoRoot, path)
		for i, match := range jsonFenceRe.FindAllStringSubmatch(string(content), -1) {
			var value interface{}
			if err := json.Unmarshal([]byte(match[1]), &value); err != nil {
				t.Errorf("%s json example %d is invalid: %v", relPath, i+1, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk markdown json examples: %v", err)
	}
}

func TestPublicYAMLExamplesAreValid(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	yamlFenceRe := regexp.MustCompile("(?s)```ya?ml\n(.*?)\n```")

	err := walkPublicTextFiles(repoRoot, []string{".md"}, func(path string) error {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(repoRoot, path)
		for i, match := range yamlFenceRe.FindAllStringSubmatch(string(content), -1) {
			var value interface{}
			if err := yaml.Unmarshal([]byte(match[1]), &value); err != nil {
				t.Errorf("%s yaml example %d is invalid: %v", relPath, i+1, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk markdown yaml examples: %v", err)
	}
}

func walkPublicTextFiles(repoRoot string, extensions []string, visit func(path string) error) error {
	allowed := make(map[string]struct{}, len(extensions))
	for _, ext := range extensions {
		allowed[ext] = struct{}{}
	}

	return filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "node_modules" || name == "simphony_workspaces" || name == "symphony_workspaces" || name == ".simphony" || path == filepath.Join(repoRoot, "dashboard", "dist") {
				return filepath.SkipDir
			}
			return nil
		}
		if matched, _ := filepath.Match("WORKFLOW-*.md", entry.Name()); matched {
			return nil
		}
		if _, ok := allowed[filepath.Ext(path)]; !ok {
			return nil
		}
		return visit(path)
	})
}

func shouldSkipMarkdownTarget(target string) bool {
	if target == "" || strings.HasPrefix(target, "#") {
		return true
	}
	lower := strings.ToLower(target)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "app://")
}
