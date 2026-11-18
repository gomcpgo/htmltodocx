package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "page.html")
	if err := os.WriteFile(in, []byte(`<h1>Hi</h1><img src="missing.png" alt="m">`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Default output name, flags after the input file, warnings on stderr.
	var stderr bytes.Buffer
	if err := run([]string{in, "-page", "letter", "-landscape"}, nil, nil, &stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "page.docx"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("PK")) {
		t.Error("output is not a zip file")
	}
	if !strings.Contains(stderr.String(), "warning: image:") {
		t.Errorf("expected an image warning, got %q", stderr.String())
	}

	// Standard input to standard output, quietly.
	var stdout bytes.Buffer
	stderr.Reset()
	if err := run([]string{"-q", "-"}, strings.NewReader("<p>x</p>"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(stdout.Bytes(), []byte("PK")) || stderr.Len() != 0 {
		t.Errorf("stdout %d bytes, stderr %q", stdout.Len(), stderr.String())
	}

	for _, args := range [][]string{{}, {in, in}, {in, "-page", "b5"}, {in, "-css", "some"}, {filepath.Join(dir, "nope.html")}} {
		if err := run(args, nil, &stdout, &stderr); err == nil {
			t.Errorf("run(%v) should fail", args)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("expected only the input and output in %s, found %d entries", dir, len(entries))
	}
}
