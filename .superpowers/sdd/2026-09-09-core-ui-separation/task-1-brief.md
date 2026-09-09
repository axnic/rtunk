### Task 1: Promote `internal/diagnostic` → `pkg/diagnostic`

**Files:**
- Move: `internal/diagnostic/diagnostic.go` → `pkg/diagnostic/diagnostic.go`
- Modify (import path only, no logic change): `cmd/rtunk/main.go`, `internal/autofix/autofix.go`, `internal/autofix/autofix_test.go`, `internal/ignore/filter.go`, `internal/ignore/ignore_test.go`, `internal/output/{gitleaks,markdownlint,regex,sarif,taplo}.go`, `internal/output/sarif_test.go`, `internal/report/report.go`, `pkg/linter/linter.go`

**Interfaces:**
- Produces: `pkg/diagnostic.Diagnostic`, `pkg/diagnostic.Fix`, `pkg/diagnostic.Severity`, `pkg/diagnostic.ParseSeverity(string) (Severity, error)` — same names/fields as today's `internal/diagnostic`, every later task imports this path.

- [ ] **Step 1: Move the package**

```bash
git mv internal/diagnostic pkg/diagnostic
```

- [ ] **Step 2: Rewrite every import of the old path**

```bash
grep -rl 'rtunk/internal/diagnostic' --include='*.go' . | xargs sed -i '' 's#rtunk/internal/diagnostic#rtunk/pkg/diagnostic#g'
```

- [ ] **Step 3: Verify the package doc comment still makes sense at its new location**

Open `pkg/diagnostic/diagnostic.go:1` and confirm the comment (`// Package diagnostic defines the normalized issue structure every linter output is converted to.`) needs no wording change — it doesn't reference `internal/`, leave as-is.

- [ ] **Step 4: Build and test**

```bash
go build ./... && go test ./...
```

Expected: builds clean, all existing tests pass (no behavior changed, only an import path).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: promote internal/diagnostic to pkg/diagnostic"
```

---

