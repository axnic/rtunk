package autofix

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
)

func TestRender_ShowsDiffLines(t *testing.T) {
	fix := diagnostic.Fix{
		Path:   "main.go",
		Before: []byte("package main\n\nfunc   main ( ) {}\n"),
		After:  []byte("package main\n\nfunc main() {}\n"),
	}
	out, err := Render(fix)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "-func   main ( ) {}") {
		t.Errorf("expected the old line marked removed, got %q", out)
	}
	if !strings.Contains(out, "+func main() {}") {
		t.Errorf("expected the new line marked added, got %q", out)
	}
}

func TestPrompt(t *testing.T) {
	cases := []struct {
		input string
		want  Choice
	}{
		{"\n", Yes},
		{"y\n", Yes},
		{"n\n", No},
		{"all\n", All},
		{"none\n", None},
		{"garbage\n", No},
	}
	for _, c := range cases {
		r := bufio.NewReader(strings.NewReader(c.input))
		var out bytes.Buffer
		got, err := Prompt(r, &out, "Apply?")
		if err != nil {
			t.Fatalf("input %q: %v", c.input, err)
		}
		if got != c.want {
			t.Errorf("input %q: got %v, want %v", c.input, got, c.want)
		}
	}
}

func TestPrompt_EOFDefaultsToNo(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	var out bytes.Buffer
	got, err := Prompt(r, &out, "Apply?")
	if err != nil {
		t.Fatal(err)
	}
	if got != No {
		t.Errorf("expected No on EOF, got %v", got)
	}
}
