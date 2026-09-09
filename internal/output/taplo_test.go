package output

import "testing"

// realTaploOutput is an actual `taplo lint --colors=never` payload (from stderr,
// interleaved with tracing INFO/ERROR log lines the parser must ignore).
const realTaploOutput = ` INFO taplo:lint_files:collect_files: found files total=1 excluded=0
error: conflicting keys
  ┌─ /private/tmp/scratch/bad.toml:4:2
  │
1 │ [package]
  │  ------- duplicate found here
  ·
4 │ [package]
  │  ^^^^^^^ duplicate key

ERROR taplo:lint_files: invalid file error=semantic errors found path="/private/tmp/scratch/bad.toml"
ERROR operation failed error=some files were not valid
`

func TestParseTaplo(t *testing.T) {
	diags, err := ParseTaplo(realTaploOutput, "taplo", "lint")
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic (log lines ignored), got %d: %+v", len(diags), diags)
	}
	d := diags[0]
	if d.Path != "/private/tmp/scratch/bad.toml" || d.Line != 4 || d.Col != 2 || d.Message != "conflicting keys" {
		t.Errorf("unexpected diagnostic: %+v", d)
	}
}

func TestParseTaplo_NoErrors(t *testing.T) {
	diags, err := ParseTaplo("", "taplo", "lint")
	if err != nil || len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %+v, err %v", diags, err)
	}
}

func TestParseTaplo_MultipleErrors(t *testing.T) {
	raw := `error: invalid TOML
  ┌─ /tmp/bad2.toml:1:8
  │
1 │ name = "test
  │        ^ unexpected token

error: invalid TOML
  ┌─ /tmp/bad2.toml:1:9
  │
1 │ name = "test
  │         ^^^^ expected value
`
	diags, err := ParseTaplo(raw, "taplo", "lint")
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics, got %d: %+v", len(diags), diags)
	}
	if diags[0].Col != 8 || diags[1].Col != 9 {
		t.Errorf("unexpected columns: %+v / %+v", diags[0], diags[1])
	}
}
