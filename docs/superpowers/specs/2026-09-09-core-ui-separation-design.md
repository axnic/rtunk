# Séparation core (API publique) / CLI (internal) — design

Date : 2026-09-09
Statut : validé, en attente de plan d'implémentation

## 1. Contexte et problème

Le core de rtunk (résolution de config, plugins, tools, exécution des
linters) vit presque entièrement en `pkg/`, sauf trois morceaux restés en
`internal/` par accident historique : `diagnostic`, `ignore`, `output`. Deux
fuites concrètes en résultent aujourd'hui :

- `pkg/linter.Linter.Run(...)` retourne `[]diagnostic.Diagnostic` — un type
  `internal/`, donc pas nommable par un consommateur externe du module.
- `pkg/linter.NewLinter(...)` prend un `*progress.Tracker` en paramètre —
  `internal/progress.Tracker` est un writer terminal (barre ANSI), donc de
  l'UX codée en dur dans le constructeur du moteur d'exécution.

Par ailleurs, l'orchestration d'un run (`resolveWork`, `loadDefinitions`,
`countJobs`, la boucle `errgroup`/semaphore) vit uniquement dans
`cmd/rtunk/main.go` — inutilisable par quoi que ce soit d'autre que le CLI
lui-même.

Objectif : que `pkg/` soit un socle 100% Go pur, sans dépendance à quoi que
ce soit de présentation (terminal, prompts, écriture de logs...), utilisable
comme librairie pour travailler avec les ressources trunk directement via
API. Que `internal/` ne contienne plus que ce qui est vraiment spécifique à
l'UX de ce binaire CLI.

Ce document remplace, pour la partie disposition des packages, le schéma de
`SPECS.md` §5/§5.1 (écrit avant que le projet ne diverge vers du `pkg/`).
Le reste de `SPECS.md` (modèle d'exécution §7, structure `Diagnostic` §8,
ignore §9) reste valide en comportement — seule l'organisation physique des
packages change.

## 2. Arborescence cible

`pkg/config`, `pkg/plugin`, `pkg/shim`, `pkg/runtime`, `pkg/tool`, `pkg/download`,
`pkg/cache` et `pkg/workspace` sont déjà à plat sous `pkg/` (un passage
antérieur, hors de ce document, a déplacé `pkg/download` hors de `pkg/plugin`
et supprimé le dossier `plugins/` embarqué — pas de nesting `pkg/trunk/*` ni
`pkg/util/download` : ça n'apportait rien de plus que la séparation déjà
obtenue, donc laissé de côté). Seul ce qui reste ci-dessous bouge :

```
pkg/
├── config/ plugin/ shim/ runtime/ tool/ download/ cache/ workspace/  (inchangés)
├── worker/                ← pkg/linter + internal/ignore + internal/output (fusionnés)
└── diagnostic/              ← internal/diagnostic (promu, type de retour public de worker)

internal/                      (CLI-only, rien ne bouge sauf ce qui est listé)
├── progress/                  (barre de progression terminal — consomme désormais worker.Event)
├── report/                    (formatage texte des diagnostics + exit code)
├── autofix/                   (diff + prompt Y/n/all/none)
└── gitutil/                   (résolution racine repo / fichiers changés — appelé par cmd/rtunk et pkg/workspace en interne, ne fuite dans aucune signature publique, reste internal)
```

## 3. `pkg/worker` : fusion linter + ignore + output

Un seul type par linter, `Runner` (1 `Runner` = 1 `LinterDefinition`) :

```go
package worker

// Event est un sum type (interface + types concrets, pas de struct à
// champs optionnels) — chaque implémentation ne porte que les données
// pertinentes à son cas.
type Event interface{ isEvent() }

type JobStarted     struct{ Target string }
type JobDone         struct{ Target string }
type DiagnosticFound struct{ diagnostic.Diagnostic }
type FixProposed     struct{ diagnostic.Fix }
// FileChanged reports one file an `in_place: true` command rewrote directly
// (e.g. `gofmt -w`) — the equivalent, for in_place commands, of FixProposed
// for `output: rewrite` ones. Without it there would be no way for `rtunk
// fmt` to print "reformatted <path>" for in_place tools, since in_place
// commands apply their own change and report no diff to preview.
type FileChanged     struct{ Path string }
type RunError        struct {
	Target string
	Err    error
}

// Definition décrit quoi lancer et comment — le "recipe" statique du
// linter, repris tel quel de l'actuel pkg/linter.Linter (def, lintersDir,
// runtimes, ProjectCacheDir).
type Definition struct {
	Linter     config.LinterDefinition
	LintersDir string
	Runtimes   map[string]shim.Shim
	// ProjectCacheDir — repris tel quel de l'actuel Linter.ProjectCacheDir.
}

type Runner struct {
	def Definition
	sem *semaphore.Weighted // partagé entre tous les Runner d'un même run (voir §5)
}

func NewRunner(def Definition, sem *semaphore.Weighted) *Runner

// RunScope décrit sur quoi lancer le Runner : la racine du repo, les
// fichiers ciblés, les règles d'ignore de config, et le mode (commandes
// formatter ou non).
type RunScope struct {
	Root        string
	Targets     []string
	IgnoreRules []config.LintIgnore
	Formatter   bool
}

// Run filtre scope.Targets via lint.ignore (matchesConfigIgnore/filterPaths,
// repris d'internal/ignore/config.go — désormais non-exportés, détail
// interne de worker), lance les commandes de def.Linter dont Formatter ==
// scope.Formatter (un process par fichier ou un process batché selon le
// grouping de la commande — logique reprise telle quelle de l'actuel
// Linter.Run/group), parse chaque sortie (internal/output, repris tel
// quel, non-exporté) en diagnostic.Diagnostic — et streame chaque résultat
// sur le channel retourné au fur et à mesure (un DiagnosticFound par
// diagnostic, un FixProposed par fix proposé par une commande "rewrite").
// Le channel est fermé une fois tous les jobs terminés.
//
// Les directives rtunk-ignore inline (internal/ignore/directive.go+filter.go)
// NE bougent PAS dans Run : la note "ignore-does-nothing" qu'elles émettent
// n'a de sens qu'avec une vue globale sur les diagnostics de TOUS les
// linters d'un fichier donné (un `rtunk-ignore` sans nom de linter peut
// supprimer le diagnostic du linter A pendant que le Runner du linter B, sur
// le même fichier, ne verrait que ses propres diagnostics et croirait la
// directive inutilisée). Cette logique reste un unique passage global,
// exposé en une fonction publique `worker.FilterDirectives(root string,
// targets []string, diags []diagnostic.Diagnostic) ([]diagnostic.Diagnostic, error)`
// (renommage direct de l'actuel `ignore.FilterAll`) appelée une seule fois
// côté CLI, après que tous les Runner d'un run ont fini d'émettre leurs
// DiagnosticFound — même point d'appel qu'aujourd'hui.
//
// L'erreur retournée en synchrone ne couvre que l'échec de préflight (ex:
// shim introuvable pour def.Linter.Runtime) — un échec par job individuel
// (crash du process sur un fichier donné, sortie non parsable) devient un
// RunError sur le channel plutôt que d'interrompre le run : la règle est
// "si c'est prévisible que ça va aussi échouer pour les autres jobs, c'est
// vérifié en préflight avant d'ouvrir le channel ; sinon c'est localisé à
// ce job et les autres continuent".
func (r *Runner) Run(scope RunScope) (<-chan Event, error)

// CountJobs reste identique à l'actuel Linter.CountJobs — prépasse pour
// dimensionner la barre de progression avant de lancer le run réel.
func (r *Runner) CountJobs(c config.Command, targets []string) int
```

`MatchesConfigIgnore`, `FilterPaths`, `Apply`, `FilterAll`, `ParseDirectives`,
`ParseRegex`, `ParseSarif`, etc. perdent leur export : plus aucun appelant
en dehors de `pkg/worker` n'en a besoin, `Runner.Run` est le seul point
d'entrée. Réduction nette de la surface d'API publique — signe que c'est la
bonne coupe.

## 4. `pkg/diagnostic`

Simple promotion d'`internal/diagnostic` (types `Diagnostic`, `Fix`,
`Severity`, `ParseSeverity`) — aucun changement de contenu, juste
l'emplacement, nécessaire puisque c'est le type que `worker.Event` expose
publiquement.

## 5. Concurrence

Inchangée dans sa forme. `cmd/rtunk/main.go` crée un seul
`*semaphore.Weighted` (taille `--jobs`), partagé entre tous les `Runner`
d'un run. La boucle actuelle (`errgroup.Group` sur `items`, une goroutine
par linter dans `checkCmd`/`fmtCmd`) reste côté CLI ; chaque goroutine
appelle désormais `runner.Run(...)` et range le channel retourné au lieu
d'attendre un retour synchrone.

## 6. Consommation côté CLI

`cmd/rtunk/main.go` (`checkCmd`/`fmtCmd`) : pour chaque item, lance
`runner.Run(...)` dans sa goroutine, puis pour chaque `Event` reçu :

- `JobStarted`/`JobDone` → `internal/progress.Tracker.Start`/`Done`
- `DiagnosticFound` → accumulé dans une slice
- `FixProposed` → accumulé, puis passé à `internal/autofix` (diff + prompt Y/n/all/none, logique actuelle d'`applyFixes` inchangée)
- `FileChanged` → accumulé dans la même slice que les chemins passés par `applyFixes` (`fmt`), imprimé `reformatted <path>`
- `RunError` → affiché en warning sur stderr (remplace le `fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)` actuel sur l'erreur de `Run`)

`checkCmd` seul : une fois tous les `Runner` terminés, la slice de
`DiagnosticFound` accumulée passe par `worker.FilterDirectives(root, targets, diags)`
(voir §3) avant `internal/report.Print` — même ordre qu'aujourd'hui
(`ignore.FilterAll` puis `report.Print`), juste le nom du paquet qui change.

`internal/progress`, `internal/report`, `internal/autofix` : aucun
changement de logique interne, seulement leur point d'appel (consomment le
channel au lieu de recevoir un writer/slice directement).

## 7. Ce qui ne change pas

- `pkg/workspace.Resolve` : signature et logique inchangées (construit déjà
  `[]config.LinterDefinition` + runtimes/tools résolus).
- `pkg/cache` : inchangé.
- `cmd/rtunk-explorer` : inchangé, ne touche ni `worker` ni `diagnostic`.
- Comportement fonctionnel de check/fmt (résolution des cibles, formats de
  sortie supportés, règles d'ignore, flow d'autofix) : identique, seule
  l'organisation du code change.

## 8. Hors scope

- Pas de nouveau consommateur de la lib écrit dans cette passe (pas de
  serveur, pas de second binaire) — seul `cmd/rtunk` migre vers la nouvelle
  API, ce qui prouve déjà qu'elle est utilisable depuis l'extérieur de
  `pkg/`.
- Mise à jour du schéma `SPECS.md` §5/§5.1 : à faire dans une passe
  documentation séparée, pas dans le plan d'implémentation de ce refactor.
