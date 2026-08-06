package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadEnvFilesKeepsExistingValues(t *testing.T) {
	root := t.TempDir()
	contents := "# comment\nexport FIRST=one\nSECOND=\"two\"\nTHIRD='three'\nPRESET=file\nnot a pair\n1BAD=value\n"
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRESET", "environment")
	for _, name := range []string{"FIRST", "SECOND", "THIRD"} {
		t.Cleanup(func() { _ = os.Unsetenv(name) })
	}

	if err := LoadEnvFiles(root); err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]string{"FIRST": "one", "SECOND": "two", "THIRD": "three", "PRESET": "environment"} {
		if actual := os.Getenv(name); actual != expected {
			t.Fatalf("%s = %q, want %q", name, actual, expected)
		}
	}
	if _, set := os.LookupEnv("1BAD"); set {
		t.Fatal("LoadEnvFiles accepted an invalid key")
	}
}

func TestLoadEnvFilesIgnoresMissingFiles(t *testing.T) {
	if err := LoadEnvFiles(t.TempDir()); err != nil {
		t.Fatal(err)
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
