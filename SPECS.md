# rtunk — Document de specs

**Metalinter CLI open-source, alternative à trunk.io Code Quality CLI**

Version du document : 1.0
Statut : Draft de démarrage projet

---

## 1. Résumé exécutif

`rtunk` est un CLI open-source qui orchestre des dizaines de linters, formatters et scanners de sécurité existants (ESLint, Prettier, Ruff, golangci-lint, clang-tidy, semgrep, gitleaks, etc.) derrière une interface unique et une configuration déclarative versionnée (`.rtunk/rtunk.yaml`).

Il ne réimplémente aucune règle de lint : c'est un **orchestrateur**. Sa valeur ajoutée est dans trois briques :

1. Un **moteur d'exécution** générique capable de lancer n'importe quel binaire de lint avec la bonne cible, le bon environnement, le bon working directory, et de gérer cache + concurrence.
2. Un **système de normalisation des outputs** : chaque linter a son propre format de sortie (texte libre, JSON propriétaire, SARIF...) ; rtunk les convertit tous vers une structure de diagnostic interne unique.
3. Une **gestion de version reproductible** : les linters et leurs versions sont pinnées dans la config, installés hermétiquement, partagés par toute l'équipe.

Ce document sert de base de démarrage : architecture, choix techniques, schéma de configuration, modèle d'exécution, système de parsing des outputs, et roadmap priorisée.

---

## 2. Règles de conception (contraintes du projet)

Ces règles sont non négociables et doivent être respectées dès le premier commit :

### 2.1 Zéro télémétrie

- Aucun appel réseau non explicitement déclenché par l'utilisateur. Pas de ping au démarrage, pas de "phone home", pas de crash reporting automatique, pas d'ID d'installation généré et envoyé où que ce soit.
- Les seuls appels réseau autorisés sont :
  - le téléchargement des binaires de linters/runtimes déclarés dans la config (vers leurs sources officielles — GitHub Releases, PyPI, npm, etc.) ;
  - `rtunk upgrade`, exécuté manuellement, qui interroge l'API GitHub Releases du repo `rtunk` lui-même ;
  - la résolution de plugins distants (`plugins.sources`), qui clone un repo git explicitement listé dans la config.
- Pas de compte, pas de login, pas de notion d'organisation cloud. Tout ce qui dans trunk.io relève de la plateforme SaaS (Merge Queue, Flaky Tests, dashboard web, `trunk login`/`whoami`) est **hors scope** : `rtunk` est un outil 100% local.
- Le code doit rendre ceci vérifiable : pas de dépendance à un SDK d'analytics (Segment, Amplitude, Sentry, etc.) dans `go.mod`. Un test CI dédié (`make audit-network`) peut scanner les imports pour l'attester.
- Toute future feature qui impliquerait un appel réseau non trivial doit être **opt-in explicite** et documentée dans le CHANGELOG.

### 2.2 Cache facilement pilotable

Le cache doit avoir un emplacement par défaut raisonnable, mais **override trivial** à 3 niveaux (ordre de priorité décroissante) :

1. Flag CLI : `--cache-dir <path>` (toutes commandes).
2. Variable d'environnement : `RTUNK_CACHE_DIR`.
3. Champ de config : `cache.dir` dans `rtunk.yaml`.
4. Défaut : `$XDG_CACHE_HOME/rtunk` (Linux), `~/Library/Caches/rtunk` (macOS), `%LOCALAPPDATA%\rtunk\cache` (Windows).

Commandes dédiées :

| Commande                           | Effet                                                                                      |
| ---------------------------------- | ------------------------------------------------------------------------------------------ |
| `rtunk cache path`                 | Affiche le chemin du cache actif (utile pour scripts/CI)                                   |
| `rtunk cache du`                   | Affiche la taille du cache, décomposée par catégorie (tools, downloads, résultats de lint) |
| `rtunk cache prune`                | Supprime les entrées non utilisées depuis N jours                                          |
| `rtunk cache clean --all`          | Vide entièrement le cache                                                                  |
| `rtunk cache clean --only=results` | Vide sélectivement une catégorie                                                           |

Le cache est strictement **local et déplaçable** : aucune donnée de cache n'est jamais uploadée. Copier le dossier de cache d'une machine à l'autre doit fonctionner sans effet de bord (cache content-addressed, pas de chemins absolus codés en dur dans les clés).

---

## 3. Choix techniques

### 3.1 Langage : Go

Voir arbitrage déjà discuté : Go est retenu pour la facilité de build/cross-compilation (binaire statique unique, `GOOS`/`GOARCH`, pas de linker externe), la rapidité de compilation, et l'adéquation avec un outil dont le travail principal est de l'orchestration de process (exec, pipes, timeouts) plutôt que du calcul intensif.

### 3.2 Stack proposée

| Besoin            | Librairie                                                                                                           | Justification                                                                                                   |
| ----------------- | ------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| CLI framework     | `spf13/cobra`                                                                                                       | Standard de facto, sous-commandes, aide auto-générée                                                            |
| Config binding    | `spf13/viper` **ou** `knadh/koanf`                                                                                  | koanf préféré : plus léger, moins de surface, meilleur contrôle du merge de config                              |
| YAML              | `goccy/go-yaml`                                                                                                     | Plus rapide et plus proche du comportement YAML 1.1 attendu que `gopkg.in/yaml.v3`, meilleur support des ancres |
| Git               | `go-git/go-git` (pur Go) + fallback `exec.Command("git", ...)` pour les cas non couverts                            | Éviter une dépendance dure à `libgit2`/cgo pour garder le build simple                                          |
| Concurrence       | `golang.org/x/sync/errgroup` + un pool borné (semaphore)                                                            | Contrôle de `max_concurrency` par linter et global                                                              |
| Schema validation | `santhosh-tekuri/jsonschema` (config validée via un schema JSON généré depuis les structs Go, `invopop/jsonschema`) | Permet de publier un `rtunk-schema.json` pour l'autocomplétion IDE                                              |
| Release/build     | `goreleaser`                                                                                                        | Cross-build multi-plateformes + publication GitHub Releases + Homebrew tap en un seul pipeline CI               |
| Tests             | stdlib `testing` + `testify/assert` + tests d'intégration en boîte noire (binaire compilé, repo git de fixtures)    |                                                                                                                 |

### 3.3 Non-dépendances volontaires

- Pas de runtime embarqué (pas de VM JS/Python dans le binaire) : Node/Python sont des _runtimes managés_ téléchargés à la demande, exactement comme les linters eux-mêmes.
- Pas de base de données : le cache est un système de fichiers content-addressed, pas de SQLite/BoltDB nécessaire au départ (peut être introduit plus tard pour indexer le cache si besoin de perf).

---

## 4. Nommage et conventions du projet

| Élément                                    | Valeur                                                                      |
| ------------------------------------------ | --------------------------------------------------------------------------- |
| Nom du projet / binaire                    | `rtunk`                                                                     |
| Dossier de config repo                     | `.rtunk/`                                                                   |
| Fichier de config principal                | `.rtunk/rtunk.yaml`                                                         |
| Fichier d'overrides local (git-ignoré)     | `.rtunk/user.yaml`                                                          |
| Directive d'ignore inline                  | `rtunk-ignore(linter/rule): raison` (compat `trunk-ignore(...)`, voir §9.1) |
| Variable d'env cache                       | `RTUNK_CACHE_DIR`                                                           |
| Variable d'env home (tools/config globale) | `RTUNK_HOME` (défaut `~/.rtunk`)                                            |
| Module Go                                  | `github.com/<org>/rtunk`                                                    |

---

## 5. Architecture générale

```
                          ┌──────────────────┐
                          │   cmd/rtunk       │   (cobra: init, check, fmt,
                          │   (CLI entrypoint)│    tools, actions, plugins,
                          └────────┬──────────┘    cache, upgrade, config)
                                   │
                 ┌─────────────────┼──────────────────┐
                 ▼                 ▼                  ▼
        ┌─────────────┐   ┌───────────────┐   ┌───────────────┐
        │ internal/    │   │ internal/     │   │ internal/     │
        │ config       │   │ plugin        │   │ gitutil       │
        │ (load+merge) │   │ (sources,     │   │ (changed      │
        │              │   │  resolution)  │   │  files, refs) │
        └──────┬───────┘   └──────┬────────┘   └──────┬────────┘
               │                  │                    │
               └─────────┬────────┴────────────────────┘
                          ▼
                 ┌──────────────────┐
                 │ internal/registry │  (fusion des définitions
                 │ (linters/tools/   │   linters+tools+actions,
                 │  actions résolus) │   résolution suggest_if)
                 └────────┬──────────┘
                          ▼
        ┌────────────────────────────────────┐
        │        internal/exec                │  (résolution ${target},
        │  (moteur d'exécution des commandes) │   ${workspace}, run_from,
        └────────┬────────────────┬───────────┘   batching, concurrency)
                 │                │
                 ▼                ▼
     ┌───────────────────┐  ┌───────────────────┐
     │ internal/tools     │  │ internal/output     │
     │ (download/install/ │  │ (parsers: sarif,    │
     │  shims/versioning) │  │  regex, pass_fail,  │
     └───────────────────┘  │  rewrite, custom...) │
                             └──────────┬───────────┘
                                        ▼
                             ┌───────────────────┐
                             │ internal/diagnostic │  (structure normalisée
                             │                     │   + fingerprinting)
                             └──────────┬───────────┘
                                        ▼
                             ┌───────────────────┐
                             │ internal/ignore     │  (rtunk-ignore inline,
                             │                     │   ignore paths config)
                             └──────────┬───────────┘
                                        ▼
                             ┌───────────────────┐
                             │ internal/report     │  (terminal UI, exit code,
                             │                     │   prompts d'autofix)
                             └───────────────────┘

        ┌───────────────┐        ┌───────────────┐
        │ internal/cache │        │ internal/     │
        │ (downloads,    │        │ actions        │  (Phase 2)
        │  résultats)    │        │ (githooks,     │
        └───────────────┘        │  triggers)      │
                                  └───────────────┘
```

### 5.1 Répertoire du projet (repo layout)

```
rtunk/
├── cmd/rtunk/                 # main.go, câblage cobra
├── internal/
│   ├── config/                # chargement, merge, validation du schema
│   ├── plugin/                # résolution des plugins.sources (git clone, local, pin par ref)
│   ├── registry/               # fusion linters/tools/actions, résolution suggest_if
│   ├── exec/                   # moteur d'exécution générique des commandes
│   ├── output/                 # parsers de sortie (un fichier par type)
│   │   ├── sarif.go
│   │   ├── lsp_json.go
│   │   ├── regex.go
│   │   ├── pass_fail.go
│   │   ├── arcanist.go
│   │   ├── rewrite.go
│   │   └── custom.go           # invocation de parser externe (node/python/shell)
│   ├── diagnostic/             # struct Diagnostic + fingerprint + dédup
│   ├── ignore/                 # parsing des directives rtunk-ignore
│   ├── gitutil/                # fichiers modifiés, upstream ref, gitignore
│   ├── cache/                  # cache content-addressed (downloads + résultats)
│   ├── tools/                  # installation runtimes/linters, shims, PATH management
│   ├── actions/                # Phase 2 : githooks, triggers, historique
│   ├── upgrade/                # Phase 4 : self-update + upgrade des linters
│   └── report/                 # rendu terminal, format JSON/SARIF de sortie, exit codes
├── plugins/                    # définitions bundlées des linters "core" (voir 8.1)
│   └── linters/
│       ├── eslint/definition.yaml
│       ├── ruff/definition.yaml
│       └── ...
├── schema/
│   └── rtunk-schema.json       # généré, publié pour l'autocomplétion IDE
├── docs/
├── e2e/                        # tests d'intégration boîte noire
├── .goreleaser.yaml
├── go.mod
└── README.md
```

---

## 6. Schéma de configuration (`.rtunk/rtunk.yaml`)

Format YAML, auto-descriptif, généré initialement par `rtunk init`, mergé depuis (dans l'ordre, chaque niveau pouvant override le précédent) :

1. Définitions bundlées dans le binaire (`plugins/` — le set "core" livré avec rtunk).
2. Plugins distants déclarés dans `plugins.sources` (dans l'ordre de déclaration).
3. `.rtunk/rtunk.yaml` du repo (source de vérité versionnée).
4. `.rtunk/user.yaml` (local, git-ignoré, overrides personnels).
5. Flags CLI (priorité maximale).

Le merge est un **merge par clé** : un override ne redéfinit que les champs qu'il spécifie ; les séquences (listes) doivent être réécrites entièrement (pas de merge additif implicite — ce choix évite les surprises silencieuses).

### 6.1 Exemple complet annoté

```yaml
version: 0.1 # version du schéma de ce fichier

cli:
  version: 1.0.0 # version de rtunk que ce repo attend
  options:
    - commands: [ALL]
      args: []
    - commands: [check, fmt]
      args: ["-y"]

cache:
  dir: "" # vide = défaut XDG ; sinon override explicite
  result_ttl: 24h # durée de vie des résultats de lint non idempotents

repo:
  trunk_branch: main # branche de référence pour le diff git-aware
  remote_hint: "" # ex: github.com/org/repo, pour désambiguïser les remotes
  use_branch_upstream: false # comparer à l'upstream de la branche courante plutôt qu'à trunk_branch

runtimes:
  enabled:
    - node@20.11.0
    - python@3.11.4

plugins:
  sources:
    - id: rtunk-core # sourcé implicitement en premier, override possible
      uri: https://github.com/<org>/rtunk-plugins
      ref: v1.0.0

lint:
  definitions: # linters custom ou overrides de linters existants
    - name: my-internal-linter
      files: [ALL]
      runtime: "" # nom d'un runtime managé (node/python/ruby/go...) requis par le binaire du linter lui-même, vide = binaire autonome
      commands:
        - name: lint
          run: ${workspace}/scripts/my-linter.sh ${target}
          output: regex
          parse_regex: "(?P<path>.*):(?P<line>\\d+): (?P<message>.*)"
          success_codes: [0, 1]
          read_output_from: stdout
  enabled:
    - eslint@8.57.0
    - ruff@0.4.2
    - gofmt@1.22.0
    - gitleaks@8.18.2
  disabled:
    - rufo
  ignore:
    - linters: [ALL]
      paths:
        - "**/generated/**"
        - "!**/generated/**/*.keep" # négation : ne pas ignorer ces fichiers-là
    - linters: [eslint]
      paths:
        - "legacy/**"
  triggers:
    - linters: [ansible-lint]
      paths: [ansible/]
      targets: [ansible/]

tools:
  enabled:
    - bazel@6.0.0
  definitions:
    - name: gh
      download: gh
      known_good_version: 2.45.0
      shims: [gh]

actions: # Phase 2
  enabled:
    - rtunk-fmt-pre-commit
    - rtunk-check-pre-push
  disabled:
    - rtunk-upgrade-available # exemple : désactiver la notif d'upgrade dispo

telemetry: false # champ figé à false, non modifiable, présent pour lisibilité/documentation
```

### 6.2 Champs racine

| Clé        | Description                                                          |
| ---------- | -------------------------------------------------------------------- |
| `version`  | Version du schéma `rtunk.yaml`                                       |
| `cli`      | Version attendue du binaire + arguments par défaut par commande      |
| `cache`    | Configuration du cache (voir §2.2)                                   |
| `repo`     | Détection de la branche de référence et du remote canonique          |
| `runtimes` | Runtimes managés (node, python, ruby, go...) nécessaires aux linters |
| `plugins`  | Sources de définitions externes                                      |
| `lint`     | Définitions, activation, ignore, triggers des linters                |
| `tools`    | Outils CLI additionnels gérés (indépendants du lint)                 |
| `actions`  | Automatisations (githooks, triggers) — Phase 2                       |

---

## 7. Modèle d'exécution des linters

Chaque **définition de linter** possède un ou plusieurs **commandes**. Une commande décrit précisément quel binaire lancer, avec quels arguments, dans quel contexte, et comment interpréter son résultat.

### 7.1 Anatomie d'une commande

```yaml
commands:
  - name: lint # nom de la commande (lint, format, analyze...)
    run: ruff check --output-format json ${target}
    target: ${file} # ou "." / ${parent} / ${parent_with(pyproject.toml)}
    run_from: . # working directory d'exécution
    stdin: false
    output: sarif
    parser: # optionnel : transforme la sortie brute avant parsing
      runtime: python
      run: ${plugin}/parsers/ruff_to_sarif.py
    read_output_from: stdout # stdout | stderr | tmp_file
    success_codes: [0, 1] # OU error_codes, jamais les deux
    batch: true # regroupe plusieurs fichiers dans un seul appel
    formatter: false # true => inclus dans `rtunk fmt`
    in_place: false
    cache_results: true
    idempotent: true # false => résultat re-vérifié après cache_ttl
    max_concurrency: 0 # 0 = pas de limite spécifique
    platforms: [] # restreint à certains OS si besoin
    version: "" # contrainte de version de l'outil pour matcher cette commande
    environment:
      - name: PATH
        list: ["${linter}/bin"]
```

### 7.1bis Résolution du runtime d'un linter

Le champ `runtime` d'une définition de linter (ex: `runtime: python` pour `ruff`, cf. [ruff/plugin.yaml](https://github.com/trunk-io/plugins/blob/main/linters/ruff/plugin.yaml) référencé en §18.1) nomme un runtime managé dont le linter a besoin pour s'exécuter (distinct du `parser.runtime` qui ne concerne que le script de parsing, cf. §7.1). Résolution :

1. rtunk cherche dans `runtimes.enabled` une entrée dont le nom correspond (ex: `node@20.11.0` pour `runtime: node`).
2. Si absente, erreur de configuration explicite au chargement (fail-fast, pas de fallback vers un runtime système déjà en PATH — objectif de reproductibilité du §1).
3. Le runtime résolu est installé/mis en cache par `internal/tools` comme n'importe quel outil, et son dossier d'installation est exposé via `${runtime}` (§7.2) et injecté en tête de `PATH` avant l'exécution de `run`.
4. `runtime: ""` (vide) signifie un binaire autonome (aucun runtime managé requis).

### 7.2 Variables de template

| Variable                         | Résolution                                                                                              |
| -------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `${workspace}`                   | Racine du repo                                                                                          |
| `${target}`                      | Fichier(s) cible(s), résolu selon `target`                                                              |
| `${file}`                        | Fichier en cours de traitement                                                                          |
| `${parent}`                      | Dossier contenant le fichier                                                                            |
| `${parent_with(<name>)}`         | Remonte l'arborescence jusqu'à trouver un dossier contenant `<name>` ; sinon le linter ne s'exécute pas |
| `${root_or_parent_with(<name>)}` | Idem mais retombe sur la racine du repo si non trouvé                                                   |
| `${linter}`                      | Dossier d'installation du linter dans le cache                                                          |
| `${runtime}`                     | Dossier d'installation du runtime (node, python...)                                                     |
| `${plugin}`                      | Racine du repo du plugin d'où vient la définition                                                       |
| `${upstream-ref}`                | Commit git de référence pour le calcul des issues nouvelles/existantes/corrigées                        |
| `${tmpfile}`                     | Fichier temporaire créé pour cette exécution si `read_output_from: tmp_file`                            |

### 7.3 Résolution des fichiers cibles (git-aware)

- Par défaut, `rtunk check` ne traite que les fichiers modifiés par rapport à `repo.trunk_branch` (détection via `git diff`).
- Respect strict de `.gitignore`.
- `--all` force le traitement de tout le repo.
- `--sample N` traite un échantillon aléatoire (utile pour évaluer un nouveau linter sur un gros repo sans tout casser).

### 7.4 Concurrence et batching

- Pool de workers borné (taille = nombre de cores par défaut, configurable via `--jobs`).
- `max_concurrency` par commande permet de limiter des outils gourmands en mémoire/licence.
- `batch: true` permet de passer plusieurs fichiers en un seul appel (`${target}` s'étend en liste), réduisant le coût de démarrage des linters lents (JVM, etc.).

### 7.5 Codes de sortie

- `success_codes`: liste des codes considérés comme "exécution réussie" (indépendamment du fait que des issues aient été trouvées).
- `error_codes`: liste des codes considérés comme "échec d'exécution" (tout le reste = succès). Un seul des deux champs doit être défini.
- `no_issues_codes`: codes qui permettent de conclure "aucune issue" sans même parser la sortie (optimisation).

### 7.6 Hold-the-line : classification new/existing/fixed et exit code

Le mécanisme "hold-the-line" de trunk n'est documenté publiquement qu'au niveau fichier ("ne lance les linters que sur les fichiers modifiés selon git", cf. §7.3) — rien de public sur une éventuelle classification plus fine par diagnostic. Un fichier modifié peut contenir des issues préexistantes sur des lignes non touchées ; le filtrage fichier seul les ferait réapparaître. rtunk va plus loin, au niveau diagnostic, et documente ce comportement comme conception propre (pas une copie de trunk, cf. §17) :

1. Calculer les diagnostics sur l'état actuel des fichiers cibles (§7.3).
2. Calculer les diagnostics de ces mêmes fichiers à `${upstream-ref}` (checkout léger en mémoire/tmp, résultat mis en cache comme toute entrée de `internal/cache` — content-addressed par contenu de fichier + version linter + config, donc rejoué gratuitement si déjà calculé).
3. Comparer par `Fingerprint` (path+code+message normalisé, insensible au numéro de ligne pour absorber les décalages) :
   - présent seulement dans l'état actuel → **new**
   - présent dans les deux → **existing** (affiché mais non bloquant par défaut)
   - présent seulement à `${upstream-ref}` → **fixed** (informatif, résumé "N issues corrigées")
4. Exit code global de `rtunk check` : non-zero si au moins un diagnostic **new** de sévérité ≥ `--fail-on` (défaut `warning`, valeurs possibles `note|warning|error`). Les issues **existing** n'affectent jamais l'exit code par défaut.
5. `--all` (§7.3) ou `--no-hold-the-line` désactive cette classification : tous les diagnostics du scope sont alors traités comme **new**.

---

## 8. Système de normalisation des outputs

C'est le cœur technique du projet : convertir n'importe quelle sortie de linter vers une structure `Diagnostic` unique.

### 8.1 Structure interne `Diagnostic`

```go
type Diagnostic struct {
    Path       string    // chemin relatif au workspace
    Line       int
    Col        int
    EndLine    int       // optionnel, pour les ranges (SARIF/LSP)
    EndCol     int
    Severity   Severity  // Note, Warning, Error (3 niveaux, mapping normalisé — voir §8.1bis)
    Code       string    // code de règle du linter (ex: no-unused-vars)
    Message    string
    LinterName string
    CommandName string
    Fingerprint string   // hash stable (path+code+message normalisé) pour hold-the-line/dédup
    Fix        *Autofix  // optionnel, si le linter propose un patch
}
```

### 8.1bis Mapping des sévérités sources

3 niveaux seulement (pas de niveau "Notice" séparé de "Note" : redondant, aucun format source n'a besoin de la distinction) :

| Format source                     | Valeur                                                                                                       | → Severity rtunk      |
| --------------------------------- | ------------------------------------------------------------------------------------------------------------ | --------------------- |
| SARIF                             | `none`, `note`                                                                                               | `Note`                |
| SARIF                             | `warning`                                                                                                    | `Warning`             |
| SARIF                             | `error`                                                                                                      | `Error`               |
| LSP (`lsp_json`)                  | `Hint`, `Information`                                                                                        | `Note`                |
| LSP (`lsp_json`)                  | `Warning`                                                                                                    | `Warning`             |
| LSP (`lsp_json`)                  | `Error`                                                                                                      | `Error`               |
| `pass_fail`                       | (issue unique, non typée)                                                                                    | `Error` par défaut    |
| `regex` (groupe nommé `severity`) | texte libre, table d'alias insensible à la casse (`err`/`error`/`E` → Error, `warn`/`warning`/`W` → Warning) | `Note` si non reconnu |

### 8.2 Pipeline d'exécution (répété pour chaque commande)

1. Exécuter `run` (avec substitution des variables de template).
2. Vérifier le code de sortie contre `success_codes`/`error_codes`. Sinon → erreur d'exécution du linter (distincte d'une issue de lint).
3. Lire la sortie brute depuis `read_output_from`.
4. **Si `parser` est défini** : lancer `parser.run` en lui donnant la sortie brute en `stdin` (ou via un runtime dédié `node`/`python` — le script écrit alors sur son propre stdout le format attendu par `output`). Vérifier que le parser retourne exit code 0.
5. Parser le résultat (sortie du parser, ou sortie brute si pas de parser) selon `output`.
6. Normaliser en `[]Diagnostic`.

### 8.3 Types d'`output` supportés (V1)

| Type                                      | Autofix | Description                                                                                                  | Priorité d'implémentation |
| ----------------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------ | ------------------------- |
| `sarif`                                   | ✓       | JSON SARIF 2.1.0 (format à privilégier, le plus riche/robuste)                                               | P0                        |
| `regex`                                   |         | Regex nommée (`parse_regex`) avec groupes `path` (obligatoire), `line`, `col`, `severity`, `code`, `message` | P0                        |
| `pass_fail`                               |         | exit 0 = OK, exit 1 = une seule issue fichier-level (message = tout le stdout)                               | P0                        |
| `rewrite`                                 | ✓       | Le linter réécrit le fichier formaté sur stdout → devient un autofix proposé/appliqué                        | P0                        |
| `lsp_json`                                |         | JSON façon Language Server Protocol (`range.start.line`, etc.)                                               | P1                        |
| `arcanist`                                |         | JSON façon Arcanist (`Path`, `Line`, `Char`, `Code`, `Description`)                                          | P1                        |
| custom (`parser`)                         | selon   | Transformation via script externe (shell/node/python) vers un des types ci-dessus                            | P1                        |
| types propres au linter (ex: JSON Clippy) | selon   | Parsers codés en dur côté rtunk pour des outils dont le JSON ne rentre dans aucun moule générique            | P2, au cas par cas        |

Règle de conception : **toujours préférer `sarif` natif** quand l'outil le supporte (c'est le format le plus robuste), sinon `regex` en dernier recours (fragile, mais universel).

### 8.4 Gestion des erreurs de parsing

- Si plusieurs groupes nommés capturent une valeur non vide dans une regex à occurrences multiples → erreur explicite de configuration (fail-fast plutôt que résultat silencieusement faux).
- Un linter qui échoue au parsing ne doit jamais faire planter toute la run `rtunk check` : l'erreur est reportée pour cette commande spécifique, les autres linters continuent.

---

## 9. Ignoring : suppressions inline et par fichier

### 9.1 Directive inline

```
// rtunk-ignore(eslint/no-unused-vars): utilisé par le hot-reload
```

Variantes : `rtunk-ignore` (une ligne), `rtunk-ignore(linter)` (tous les codes de ce linter), `rtunk-ignore-all(linter)` (fichier entier), `rtunk-ignore-begin/end(linter)` (bloc), multi-linters séparés par virgule.

**Compatibilité `trunk-ignore`** : le parser d'`internal/ignore` reconnaît `trunk-ignore(...)` / `trunk-ignore-all(...)` / `trunk-ignore-begin/end(...)` comme alias stricts des directives `rtunk-*` équivalentes. Objectif : un repo qui migre depuis trunk.io n'a pas besoin de réécrire ses milliers de commentaires d'ignore existants — les deux préfixes sont acceptés indéfiniment (pas de dépréciation prévue), et `rtunk` génère uniquement `rtunk-ignore` pour tout nouvel autofix qui ajouterait une directive. La détection de directives inutilisées (§ ci-dessous) traite les deux préfixes de façon identique.

- Détection des directives inutilisées (une issue `note`, non bloquante, du type `rtunk/ignore-does-nothing`), avec `rtunk-ignore(rtunk)` (ou `trunk-ignore(rtunk)`) pour les faire taire explicitement.
- Grammaire formelle à documenter dans `docs/ignore-syntax.md` (EBNF simple, avec les deux préfixes acceptés en entrée du token `<trunk-ignore-type>`).

### 9.2 Ignore par config

```yaml
lint:
  ignore:
    - linters: [ALL]
      paths: ["dist/**", "!dist/keep-me.js"]
```

- Globs relatifs à la racine du repo, négation via `!`.
- Respect implicite de `.gitignore` en plus de cette liste.

---

## 10. Plugins et partage de configuration

- Un plugin est un repo git contenant un ou plusieurs fichiers `plugin.yaml`, avec le même schéma que `rtunk.yaml` (à l'exception de `plugins.sources` et `cli.version`, non mergés depuis un plugin pour garder un merge prévisible).
- `plugins.sources[].ref` doit être un tag ou un SHA (jamais une branche, pour la reproductibilité).
- `rtunk plugins add <uri> [ref] --id=<id>` ajoute une source à la config.
- Le repo `rtunk-plugins` officiel (à créer, séparé du repo `rtunk` core) contient les définitions communautaires des linters supportés — permet de faire évoluer la liste des linters sans release du binaire.
- Un set minimal de définitions "core" (5-10 linters très utilisés) est bundlé directement dans le binaire pour que `rtunk` fonctionne offline dès l'installation, avant même de résoudre un plugin distant.

---

## 11. Actions (Phase 2)

- Types de triggers : `githooks` (pre-commit, pre-push, etc.), `file modification` (nécessite un daemon de monitoring, cf. §14), `manual` (`rtunk run <action>`). Le trigger `time-based` (cron) est repoussé en Phase 5+ (faible priorité, nécessite un scheduler persistant).
- `rtunk actions list|enable|disable|run|history`.
- Actions par défaut fournies : autoformat au commit, `check` au push, notification (terminale uniquement, pas de notif cloud) si une nouvelle version de `rtunk` est disponible — cette dernière ne fait un appel réseau que si l'action est activée, et reste désactivable (cf. §2.1, zéro télémétrie ≠ zéro fonctionnalité réseau opt-in).
- Installation des hooks : écrit dans `.git/hooks/`, avec un garde-fou qui détecte et n'écrase pas des hooks existants sans confirmation.

---

## 12. Init (Phase 3)

`rtunk init` :

1. Scanne le repo (types de fichiers présents, fichiers de config de linters connus).
2. Pour chaque définition disponible (bundlée + plugins déjà configurés), évalue `suggest_if` :
   - `config_present` : un des `direct_configs` du linter est trouvé sur le disque → activer.
   - `files_present` : un fichier du type géré par ce linter est trouvé → activer.
   - `never` : jamais suggéré automatiquement.
3. Génère `.rtunk/rtunk.yaml` avec les linters retenus dans `lint.enabled`.
4. Flags : `--only-detected-formatters`, `--only-detected-linters`, `--single-player-mode` (ajoute `.rtunk/` à `.git/info/exclude` plutôt qu'à `.gitignore`, pour tester localement sans impacter l'équipe).
5. `rtunk deinit` fait l'inverse proprement (retire hooks, config, cache local du repo).

---

## 13. Auto-upgrade (Phase 4)

- `rtunk upgrade` : vérifie la dernière release sur GitHub Releases du repo `rtunk` (appel réseau explicite, déclenché uniquement par cette commande — jamais en tâche de fond sans action explicite de l'utilisateur), propose de mettre à jour `cli.version` dans la config, et/ou les versions pinnées des linters dans `lint.enabled`.
- Flags : `-y`/`-n` (répondre automatiquement), `--dry-run` (détecte sans appliquer), `--filter` (limiter à certains linters), `--apply-to <file>` (appliquer à un fichier de config spécifique, utile en CI/monorepo).
- Vérification d'intégrité : checksum (SHA256) des binaires téléchargés, publié dans chaque release, vérifié avant installation — remplace le mécanisme de "signature/provenance" propriétaire de trunk par quelque chose de simple et auditable (pas de dépendance à un serveur de vérification tiers).

---

## 14. Fonctionnalités repoussées / hors scope V1

| Feature (équivalent trunk)                                 | Décision                                                                                                                                                                             |
| ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Merge Queue, Flaky Tests, dashboard web                    | Hors scope définitivement — ce sont des produits SaaS, pas du CLI de lint local                                                                                                      |
| `login`/`logout`/`whoami`, notion d'org cloud              | Hors scope — pas de backend                                                                                                                                                          |
| Daemon de monitoring de fichiers (`rtunk daemon`)          | Repoussé après Phase 2 ; nécessaire seulement pour `auto_sync` des tools et les triggers `file modification` en continu                                                              |
| Triggers `time-based` (cron)                               | Repoussé, faible usage, peut se faire via cron système + `rtunk run` en attendant                                                                                                    |
| IDE extensions (VSCode, Neovim)                            | Repoussé après le CLI stable — dépend d'un mode `--lsp` ou JSON streaming à concevoir séparément                                                                                     |
| Signature/provenance cryptographique du binaire (`--lock`) | Remplacé en V1 par un simple checksum SHA256 (voir §13), une vraie signature (sigstore/cosign) pourrait venir plus tard                                                              |
| Hook shell PATH auto sur Windows (PowerShell/cmd)          | Hors scope pour le moment — Windows reste une cible de build/installation (§16.3), mais l'injection automatique de PATH pour les shims (§15 Phase 1) se limite à bash/zsh/fish en V1 |

---

## 15. Roadmap priorisée

Ordre de priorité imposé : **1) linter/check → 2) actions → 3) init → 4) auto-upgrade**. Le detail ci-dessous découpe chaque priorité en jalons livrables.

### 15.0 POC v0 — périmètre minimal avant Phase 1

Objectif : prouver le pipeline de bout en bout sur un vrai repo avec le plus petit diff possible, avant d'investir dans le reste de la Phase 1.

- **Linter** : exécution d'au moins un linter réel (parser `sarif` ou `regex`).
- **Formatter** : exécution d'au moins un formatter réel (`output: rewrite`, `formatter: true`).
- **Ciblage** : `rtunk check [<path>]` / `rtunk fmt [<path>]` — fichiers en diff vs `repo.trunk_branch` par défaut (§7.3), ou `<path>` explicite s'il est fourni en argument (override du mode git-aware, pas de `--all`/`--sample` requis à ce stade).
- **Config** : chargement compatible à la fois de `.rtunk/rtunk.yaml` et de `.trunk/trunk.yaml` (l'un ou l'autre, pas de merge des deux) — `.rtunk/rtunk.yaml` prioritaire s'il existe, sinon fallback sur `.trunk/trunk.yaml` pour permettre un essai sans migration.

Hors POC v0 (repoussé à la suite de la Phase 1) : commandes `cache *` dédiées, `rtunk-ignore` inline, plugins distants, `tools.definitions` génériques, actions, init, upgrade. Un cache minimal peut exister en interne (content-addressed, §16.1) sans CLI dédiée.

### Phase 1 — Moteur de lint (`check` / `fmt`) — priorité 1

Objectif : un utilisateur peut écrire un `.rtunk/rtunk.yaml` à la main, et `rtunk check`/`rtunk fmt` fonctionnent correctement sur un vrai repo.

- [ ] Chargement + validation + merge de config (`internal/config`), schema JSON publié.
- [ ] Détection git-aware des fichiers modifiés (`internal/gitutil`).
- [ ] Moteur d'exécution des commandes (`internal/exec`) : substitution de variables, `run_from`, `target`, `success_codes`/`error_codes`, `environment`, `batch`, concurrence bornée.
- [ ] Installeur de tools/runtimes (`internal/tools`) : téléchargement, vérification checksum, cache hermétique, shims + PATH dynamique (shell hooks bash/zsh/fish).
- [ ] Parsers V1 : `sarif`, `regex`, `pass_fail`, `rewrite` (P0 du §8.3).
- [ ] Structure `Diagnostic` + rendu terminal (`internal/report`) avec exit codes corrects pour CI.
- [ ] `rtunk-ignore` inline + `lint.ignore` par config (`internal/ignore`).
- [ ] Cache des résultats (`internal/cache`) avec les commandes `cache path/du/prune/clean` (§2.2).
- [ ] Set de 8-10 définitions de linters bundlées (ex : eslint, prettier, ruff, black, gofmt, golangci-lint, shellcheck, gitleaks) pour valider le moteur sur des cas réels variés (JS/TS, Python, Go, shell, secrets).
- [ ] Tests d'intégration boîte noire sur des repos fixtures (un par langage couvert).

Critère de sortie de phase : `rtunk check`/`rtunk fmt` utilisables en remplacement direct de `trunk check`/`trunk fmt` sur un repo mono-langage simple, sans régression fonctionnelle perçue.

### Phase 2 — Actions — priorité 2

- [ ] `internal/actions` : modèle de définition d'action, `enabled`/`disabled`.
- [ ] Installation/gestion des git hooks (pre-commit, pre-push, etc.).
- [ ] Trigger `manual` (`rtunk run <action>`), `rtunk actions list/history`.
- [ ] Actions par défaut : autoformat au commit, check au push.
- [ ] Notifications terminal (pas de notif cloud).
- [ ] Trigger `file modification` **seulement si** un mode simple non-daemon suffit (ex: watcher basique via `fsnotify` sur invocation explicite) ; sinon repoussé avec le daemon complet en Phase 5.

### Phase 3 — Init — priorité 3

- [ ] Scanner de repo + résolution `suggest_if` (`config_present`/`files_present`/`never`).
- [ ] Génération de `.rtunk/rtunk.yaml`.
- [ ] `--single-player-mode`, `--only-detected-formatters/linters`.
- [ ] `rtunk deinit`.

### Phase 4 — Auto-upgrade — priorité 4

- [ ] Client GitHub Releases (appel réseau uniquement sur invocation explicite).
- [ ] Vérification de checksum avant remplacement du binaire.
- [ ] Détection et application des bumps de version des linters dans la config.
- [ ] Flags `-y`/`-n`/`--dry-run`/`--filter`/`--apply-to`.

### Phase 5+ — Au fil de l'eau (hors priorité imposée)

- Daemon de monitoring pour triggers continus et `tools.auto_sync`.
- Types d'output additionnels (`lsp_json`, `arcanist`, parsers custom node/python complets).
- Repo `rtunk-plugins` communautaire séparé + processus de contribution.
- Mode `--output=json`/`--output=sarif` pour intégration CI tierce.
- Intégrations IDE (VSCode a minima).

---

## 16. Considérations non-fonctionnelles

### 16.1 Performance

- Objectif : sur un diff de taille normale (< 50 fichiers modifiés), `rtunk check` ne doit pas être perceptiblement plus lent que la somme des linters lancés manuellement — le gain vient du cache et du parallélisme, pas d'une régression de perf.
- Cache de résultats keyed sur `hash(contenu du fichier + version du linter + config effective de la commande)`.

### 16.2 Sécurité

- Téléchargements de binaires exclusivement en HTTPS, vérifiés par checksum.
- Pas d'exécution de code arbitraire au chargement de la config : les seules commandes exécutées sont celles explicitement déclarées (`run`, `parser.run`) — pas d'`eval` de YAML.
- Les plugins distants sont du code de configuration, pas du code exécuté au clone : un `git clone` à un `ref` pinné, jamais un `checkout` de branche mouvante.

### 16.3 Distribution

- `goreleaser` : binaires linux/darwin/windows × amd64/arm64, checksums SHA256, changelog auto depuis les commits conventionnels.
- Installation : script `install.sh` (curl | sh, vérifie le checksum), + formule Homebrew, + `go install` pour les devs Go.
- Pas de "launcher" séparé qui télécharge le vrai binaire à chaque run (contrairement à trunk) — trop de complexité et un point de contact réseau implicite à chaque invocation, ce qui va à l'encontre de la règle zéro-télémétrie/zéro-appel-implicite. Le binaire `rtunk` est directement l'exécutable final ; `rtunk upgrade` gère les mises à jour de façon explicite.

### 16.4 Tests & CI

- Unit tests par package (`internal/output/*_test.go` avec fixtures de sorties réelles de linters).
- Tests d'intégration : repos fixtures versionnés dans `e2e/fixtures/<langage>/`, exécution du binaire compilé, assertions sur diagnostics + exit code + fichiers réécrits.
- CI GitHub Actions : lint du projet lui-même avec... `rtunk` (dogfooding dès que Phase 1 est stable), build multi-plateforme, tests unitaires + intégration sur linux/macOS/windows.

---

## 17. Risques identifiés

| Risque                                                                                    | Impact                               | Mitigation                                                                                                                                                                                                                                        |
| ----------------------------------------------------------------------------------------- | ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Le format `regex` est fragile face aux mises à jour de linters tiers                      | Faux négatifs silencieux             | Préférer SARIF partout où possible ; tests de non-régression par linter avec sortie figée en fixture                                                                                                                                              |
| Explosion de la matrice `platforms × version` par linter                                  | Complexité de maintenance            | Documenter clairement la convention "première commande qui matche" et outiller un linter interne pour valider les définitions (`rtunk plugin-lint`)                                                                                               |
| Reproduire un schéma de config très proche de trunk.io peut créer une confusion de marque | Risque réputationnel/juridique perçu | Nommage entièrement distinct (`rtunk`, `.rtunk/`), pas de réutilisation d'assets/texte/branding trunk ; le schéma reprend des _concepts fonctionnels génériques_ (commande, target, output type) et non une copie littérale de leur documentation |
| Le daemon de monitoring (Phase 5) est complexe à faire cross-plateforme correctement      | Retard, bugs de watch sur gros repos | Le repousser explicitement après les 4 priorités, ne pas le laisser contaminer le scope de Phase 1-4                                                                                                                                              |

---

## 18. Ressources et références

### 18.1 Documentation trunk.io (référence fonctionnelle, pas de copie littérale)

Utile pour comprendre le comportement attendu par les utilisateurs migrant depuis trunk — à lire comme spec fonctionnelle, jamais à copier telle quelle (texte/branding) dans le code ou la doc de rtunk.

- [Code Quality CLI — overview](https://github.com/trunk-io/docs/blob/main/code-quality/overview/getting-started/README.md)
- [Configuration (`trunk.yaml`)](https://docs.trunk.io/code-quality/overview/getting-started/configuration/index.md)
- [Commands (définition d'une commande de linter)](https://docs.trunk.io/cli/configuration/lint/commands)
- [Output (types de sortie)](https://docs.trunk.io/references/cli/configuration/lint/output)
- [Output Parsing (parsers custom)](https://docs.trunk.io/code-quality/overview/getting-started/configuration/lint/output-parsing.md)
- [Custom linters](https://docs.trunk.io/code-quality/linters/custom-linters)
- [Auto-Enable (`suggest_if`)](https://docs.trunk.io/code-quality/overview/getting-started/configuration/lint/auto-enable.md)
- [Ignoring issues and files (`trunk-ignore`)](https://docs.trunk.io/code-quality/overview/linters/ignoring-issues-and-files.md)
- [Plugins](https://docs.trunk.io/code-quality/overview/getting-started/configuration/plugins/index.md)
- [Run Linters (`check`/`fmt`)](https://docs.trunk.io/code-quality/overview/linters/run-linters.md)
- [Actions](https://docs.trunk.io/code-quality/overview/getting-started/actions/README.md)
- [Tools](https://docs.trunk.io/code-quality/overview/getting-started/tools.md)
- [Caching](https://docs.trunk.io/code-quality/overview/getting-started/caching.md)
- [Supported Linters (liste des 100+ outils)](https://docs.trunk.io/code-quality/overview/linters/supported/index.md)
- [Index complet de la doc (llms.txt)](https://docs.trunk.io/llms.txt)
- [trunk-io/plugins (définitions YAML réelles par linter, exemples de `parser`/`output`)](https://github.com/trunk-io/plugins)
- [Exemple concret : `ruff/plugin.yaml`](https://github.com/trunk-io/plugins/blob/main/linters/ruff/plugin.yaml)
- [Exemple concret : `clippy/plugin.yaml`](https://github.com/trunk-io/plugins/blob/main/linters/clippy/plugin.yaml)
- [Exemple concret : `detekt/plugin.yaml`](https://github.com/trunk-io/plugins/blob/main/linters/detekt/plugin.yaml)
- [Exemple concret : `prettier/plugin.yaml`](https://github.com/trunk-io/plugins/blob/main/linters/prettier/plugin.yaml)
- [trunk-yaml-schema.json (schéma JSON publié par trunk, référence de structure)](https://static.trunk.io/pub/trunk-yaml-schema.json)

#### Exemple concret : sortie de `trunk init`

Capture d'une exécution réelle de `trunk init`, utile comme référence fonctionnelle pour l'UX attendue de `rtunk init` (§12) : détection des linters pertinents avec leur version et le nombre de fichiers concernés, proposition (opt-in, Y/n) d'installer les git hooks, puis proposition (opt-in, Y/n) de scanner le repo. À reprendre dans l'esprit (mêmes étapes, mêmes choix explicites Y/n) sans copier le texte ni le branding (§17) :

```
/Vo/Sp/La/rtunk › trunk init                                                                                                                                                                     11:28:44

Welcome to Trunk!

 The Trunk CLI provides multi-language static analysis, formatting, image
 optimization, and more. You can use it alone, but it's built for teams; see
 https://docs.trunk.io/check for more info.

Initializing 100% [======================================================================================================================================================================>]  12/12  2.9s
Formatting Configs 100% [================================================================================================================================================================>]  12/12  5.1s

✔ 10 linters were enabled (.trunk/trunk.yaml)

  checkov 3.3.8 (1 yaml file)
  git-diff-check (14 files)
  gofmt 1.20.4 (9 go files)
  golangci-lint2 2.12.2 (7 go files)
  grype 0.116.0 (1 lockfile file)
  markdownlint 0.45.0 (1 markdown file) (created .markdownlint.yaml)
  prettier 3.6.2 (1 markdown, 1 yaml file)
  taplo 0.10.0 (1 toml file)
  trufflehog 3.95.9 (15 files)
  yamllint 1.38.0 (1 yaml file) (created .yamllint.yaml)

→ Would you like Trunk to manage your git hooks and enable some built-in hooks? (Y/n):

✔ 3 actions were enabled (.trunk/trunk.yaml)

  trunk-announce
  trunk-check-pre-push
  trunk-fmt-pre-commit

✔ Trunk is now managing your git hooks (https://docs.trunk.io for more info)

→ Would you like Trunk to scan your repo for issues? (Y/n):

Press spacebar to skip checks
Checking 100% [=========================================================================================================================================================================>]  92/92  78.7s

Trunk found:
  ✖ 8 total lint issues
  ✖ 1 unformatted file
  ✔ no security issues

Run trunk check {file/directory} to see specific results

Next Steps

 1. Read documentation
    Our documentation can be found at https://docs.trunk.io

 2. Get help and give feedback
    Join the Trunk community at https://slack.trunk.io
```

Points à retenir pour `rtunk init` (§12) :

- Récapitulatif par linter activé : nom, version résolue, et le compte + type de fichiers ayant déclenché `suggest_if` (`config_present`/`files_present`) — pas juste la liste des noms.
- Signaler explicitement quand un fichier de config par défaut est généré pour un linter (ex: `.markdownlint.yaml`), pas seulement l'activation du linter.
- Toute proposition de git hook ou de scan reste une question Y/n distincte et opt-in — jamais d'action réseau ou de modification de `.git/hooks/` sans confirmation explicite (cohérent avec §2.1 et le garde-fou anti-écrasement du §11).
- Le scan optionnel post-init résume par catégorie (issues de lint, fichiers non formatés, issues de sécurité), pas juste un total brut, et renvoie vers `rtunk check {file/directory}` pour le détail plutôt que d'afficher toutes les issues immédiatement.
- Un pied de page "prochaines étapes" en fin d'`init` est une bonne pratique UX à reprendre, mais son contenu doit être propre à rtunk (lien doc/README/issues GitHub du projet) — pas de lien vers une communauté Slack ou un service tiers de trunk (§17, pas de copie de branding).

### 18.2 Formats de sortie et specs ouvertes

- [SARIF 2.1.0 — spec OASIS](https://docs.oasis-open.org/sarif/sarif/v2.0/sarif-v2.0.html)
- [SARIF JSON Schema officiel](https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json)
- [SARIF SDK / outils Microsoft](https://github.com/microsoft/sarif-sdk)
- [Language Server Protocol — spec (format `lsp_json`)](https://microsoft.github.io/language-server-protocol/specification)
- [re2 syntax (moteur regex recommandé pour `parse_regex`, évite le ReDoS des regex backtracking)](https://github.com/google/re2/wiki/Syntax)

### 18.3 Librairies Go pressenties

- [spf13/cobra — framework CLI](https://github.com/spf13/cobra)
- [knadh/koanf — chargement/merge de config](https://github.com/knadh/koanf)
- [goccy/go-yaml — parseur YAML](https://github.com/goccy/go-yaml)
- [go-git/go-git — git pur Go](https://github.com/go-git/go-git)
- [golang.org/x/sync (errgroup, semaphore)](https://pkg.go.dev/golang.org/x/sync)
- [santhosh-tekuri/jsonschema — validation de schéma](https://github.com/santhosh-tekuri/jsonschema)
- [invopop/jsonschema — génération de schéma JSON depuis des structs Go](https://github.com/invopop/jsonschema)
- [fsnotify/fsnotify — watch de fichiers (triggers Phase 2/5)](https://github.com/fsnotify/fsnotify)
- [goreleaser — build cross-plateforme + releases](https://goreleaser.com/)
- [testify — assertions de test](https://github.com/stretchr/testify)

### 18.4 Infra de distribution / release

- [GitHub Releases API (utilisée par `rtunk upgrade`)](https://docs.github.com/en/rest/releases/releases)
- [Homebrew — créer un tap](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap)
- [Conventional Commits (pour changelog auto)](https://www.conventionalcommits.org/)
- [Sigstore/cosign (piste future pour signature de binaire, cf. §14)](https://www.sigstore.dev/)

### 18.5 Outils de référence pour s'inspirer d'architectures similaires (metalinters/orchestrateurs OSS)

- [pre-commit (orchestrateur de hooks multi-langage, Python)](https://pre-commit.com/)
- [golangci-lint (agrégateur de linters Go, bonne référence pour le pattern "runner + cache + config unifiée")](https://golangci-lint.run/)
- [super-linter (GitHub Actions, agrégateur massif de linters)](https://github.com/super-linter/super-linter)
- [reviewdog (normalise les outputs de linters vers un format diagnostique commun, très proche de notre §8)](https://github.com/reviewdog/reviewdog)
- [Checkstyle/ESLint formatters (exemples de formats de sortie propriétaires à mapper)](https://eslint.org/docs/latest/use/formatters/)

## 19. Prochaines étapes concrètes

1. Créer le repo `rtunk` (Go module, cobra scaffolding, CI de base).
2. Écrire le schéma de config (`internal/config`) + les structs + génération du JSON schema — même avant d'avoir un seul linter qui fonctionne, ça sert de contrat stable pour le reste.
3. Implémenter le moteur d'exécution minimal + un seul parser (`regex`, le plus simple) sur un seul linter bundlé (ex: `gofmt` en `rewrite`) pour valider le pipeline de bout en bout.
4. Étendre aux autres parsers (`sarif`, `pass_fail`) et à 5-6 linters supplémentaires couvrant JS/TS/Python.
5. Cache + commandes `cache *` dès ce stade (règle non négociable du §2.2).
6. Geler l'API de `Diagnostic` et du format de sortie terminal avant d'attaquer la Phase 2.
