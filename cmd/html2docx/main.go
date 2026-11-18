// Command html2docx converts an HTML file to a Word document.
//
//	html2docx report.html                 writes report.docx
//	html2docx report.html -o out.docx
//	html2docx - -o out.docx < report.html
//
// Images and stylesheets are loaded only from the directory of the input
// file. Nothing is fetched from the network.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gomcpgo/htmltodocx"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "html2docx:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("html2docx", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "output file (default: input name with .docx; \"-\" for standard output)")
	page := fs.String("page", "a4", "page size: a4, letter or legal")
	landscape := fs.Bool("landscape", false, "landscape orientation")
	margin := fs.Float64("margin", 25.4, "page margin in millimetres")
	cssMode := fs.String("css", "all", "CSS to apply: all, inline or none")
	base := fs.String("base", "", "base URL for relative links")
	resDir := fs.String("resources", "", "directory to load images and stylesheets from (default: the input file's directory)")
	quiet := fs.Bool("q", false, "do not print warnings")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: html2docx [flags] input.html   (use - to read standard input)")
		fs.PrintDefaults()
	}

	// Accept flags before and after the input file.
	var inputs []string
	for {
		if err := fs.Parse(args); err != nil {
			if err == flag.ErrHelp {
				return nil
			}
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		inputs = append(inputs, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(inputs) != 1 {
		fs.Usage()
		return fmt.Errorf("expected one input file")
	}
	input := inputs[0]

	opts := &htmltodocx.Options{BaseURL: *base}
	switch strings.ToLower(*page) {
	case "a4":
		opts.Page.Size = htmltodocx.A4
	case "letter":
		opts.Page.Size = htmltodocx.Letter
	case "legal":
		opts.Page.Size = htmltodocx.Legal
	default:
		return fmt.Errorf("unknown page size %q", *page)
	}
	opts.Page.Landscape = *landscape
	if *margin < 0 || *margin > 200 {
		return fmt.Errorf("margin out of range")
	}
	m := htmltodocx.Mm(*margin)
	if m == 0 {
		m = 1 // all-zero margins mean "default" to the library
	}
	opts.Page.Margins = htmltodocx.Margins{Top: m, Right: m, Bottom: m, Left: m}
	switch strings.ToLower(*cssMode) {
	case "all":
		opts.CSS = htmltodocx.CSSAll
	case "inline":
		opts.CSS = htmltodocx.CSSInlineOnly
	case "none":
		opts.CSS = htmltodocx.CSSNone
	default:
		return fmt.Errorf("unknown css mode %q", *cssMode)
	}
	if !*quiet {
		opts.Warn = func(w htmltodocx.Warning) { fmt.Fprintln(stderr, "warning:", w) }
	}

	var in io.Reader
	dir := *resDir
	if input == "-" {
		in = stdin
		if *out == "" {
			*out = "-"
		}
	} else {
		f, err := os.Open(input)
		if err != nil {
			return err
		}
		defer f.Close()
		in = bufio.NewReader(f)
		if dir == "" {
			dir = filepath.Dir(input)
		}
		if *out == "" {
			*out = strings.TrimSuffix(input, filepath.Ext(input)) + ".docx"
		}
	}
	if dir != "" {
		opts.Resources = htmltodocx.DirLoader(dir)
	}

	if *out == "-" {
		w := bufio.NewWriter(stdout)
		if err := htmltodocx.Convert(context.Background(), in, w, opts); err != nil {
			return err
		}
		return w.Flush()
	}

	// Write to a temporary file first so a failed conversion does not leave
	// a truncated document behind.
	tmp, err := os.CreateTemp(filepath.Dir(*out), ".html2docx-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	w := bufio.NewWriter(tmp)
	if err := htmltodocx.Convert(context.Background(), in, w, opts); err != nil {
		tmp.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), *out)
}
