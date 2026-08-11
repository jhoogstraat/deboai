package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadKeepsProcessValuesWithoutMutatingEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Chdir(t.TempDir())
	contents := "# comment\nexport FIRST=one\nSECOND=\"two\"\nTHIRD='three'\nPRESET=file\nnot a pair\n1BAD=value\n"
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRESET", "environment")
	values, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]string{"FIRST": "one", "SECOND": "two", "THIRD": "three", "PRESET": "environment"} {
		if actual := values[name]; actual != expected {
			t.Fatalf("%s = %q, want %q", name, actual, expected)
		}
	}
	if _, set := values["1BAD"]; set {
		t.Fatal("Load accepted an invalid key")
	}
	if _, set := os.LookupEnv("FIRST"); set {
		t.Fatal("Load modified the process environment")
	}
}

func TestLoadIgnoresMissingFiles(t *testing.T) {
	if _, err := Load(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestLoadUsesDirectoriesInOrder(t *testing.T) {
	worktree := t.TempDir()
	repositoryRoot := t.TempDir()
	workingDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, ".env"), []byte("SELECTED=worktree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryRoot, ".env"), []byte("SELECTED=repository\nREPOSITORY=repository\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workingDirectory, ".env"), []byte("SELECTED=working\nREPOSITORY=working\nWORKING=working\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvFileVariable, "")

	values, err := Load(worktree, repositoryRoot, workingDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if values["SELECTED"] != "worktree" || values["REPOSITORY"] != "repository" || values["WORKING"] != "working" {
		t.Fatalf("Load() = %#v, want worktree, repository, then working-directory precedence", values)
	}
}

func TestLoadEnvFileVariableOverridesWorktreeDefaults(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		".env":       "SELECTED=default\n",
		"debo.env":   "SELECTED=secondary\n",
		"custom.env": "SELECTED=configured\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(EnvFileVariable, "custom.env")

	values, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if actual := values["SELECTED"]; actual != "configured" {
		t.Fatalf("SELECTED = %q, want configured", actual)
	}
}

func TestValueAndRequire(t *testing.T) {
	t.Setenv("PRIMARY", "")
	t.Setenv("FALLBACK", "  value  ")

	if actual := Value("PRIMARY", "FALLBACK"); actual != "value" {
		t.Fatalf("Value() = %q, want the trimmed fallback", actual)
	}
	if actual := ValueOr("default", "PRIMARY"); actual != "default" {
		t.Fatalf("ValueOr() = %q, want default", actual)
	}
	if _, err := Require("PRIMARY"); err == nil {
		t.Fatal("Require() accepted an unset variable")
	}
}

func TestListAndPairs(t *testing.T) {
	t.Setenv("ITEMS", "one, two ,,three")
	if actual := List("ITEMS"); !reflect.DeepEqual(actual, []string{"one", "two", "three"}) {
		t.Fatalf("List() = %#v", actual)
	}

	t.Setenv("MAPPING", "1=On Hold, 2=In Review, broken")
	expected := map[string]string{"1": "On Hold", "2": "In Review"}
	if actual := Pairs("MAPPING"); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Pairs() = %#v, want %#v", actual, expected)
	}
}
