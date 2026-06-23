// Package template defines the catalog of prebaked box types a user can pick
// from the interactive CLI. A template is data-only: an image plus an optional
// offline setup script that prepares the lab.
//
// Constraint: boxes run as a non-root user with no apt/sudo, so setup scripts
// must only do non-root, offline work (mkdir/echo/git-init/sqlite seed) and any
// real tooling must already exist in the chosen Image. Production-grade
// templates will use our own prebaked images later; these are demoable today.
//
// NOTE (internal): "Image" here is a container image, but this is never exposed
// to users — externally these are just "box types".
package template

// BaseImage is the warm-pool image; templates that use it get an instant claim.
const BaseImage = "ubuntu:24.04"

// Template describes one selectable box type.
type Template struct {
	ID          string `json:"id"`
	Title       string `json:"title"`    // shown in the menu
	Desc        string `json:"desc"`     // one-line menu description
	Category    string `json:"category"` // "VM" | "Learn" | "Dev"
	Image       string `json:"image"`
	SetupScript string `json:"setupScript,omitempty"` // POSIX sh, runs once when the box is warmed
	WelcomeMsg  string `json:"welcomeMsg,omitempty"`  // printed before dropping into the shell

	// Manifest is an optional free-hand Kubernetes Pod YAML. When set it is used
	// as the base for every box of this template; Boxly re-applies its safety
	// guardrails (non-root, dropped caps, SA, labels, workspace) on top, so the
	// admin controls image/env/resources/volumes without breaking isolation.
	Manifest string `json:"manifest,omitempty"`

	// Pool sizing bounds (predictive warm pool). WarmMax 0 means "use the global
	// default". Disabled templates are hidden and never warmed.
	WarmMin  int  `json:"warmMin,omitempty"`
	WarmMax  int  `json:"warmMax,omitempty"`
	Disabled bool `json:"disabled,omitempty"`

	Builtin bool `json:"builtin,omitempty"` // true for the shipped catalog (read-only id)
}

// builtins is the catalog shipped with Boxly. Admins can add custom templates on
// top of these at runtime (see the registry).
var builtins = []Template{
	{
		ID: "normal", Title: "Normal VM", Desc: "clean Ubuntu box", Category: "VM",
		Image:      BaseImage,
		WarmMin:    1, // keep one always-warm so the common case is instant
		WelcomeMsg: "Clean Ubuntu box. /work is your home — have fun.",
	},
	{
		ID: "learn-linux", Title: "Learn: Linux/Shell", Desc: "text-adventure dungeon", Category: "Learn",
		Image: BaseImage, // claims the warm pool → instant
		SetupScript: `set -e
D=/work/dungeon
mkdir -p "$D/cellar/cabinet"
echo 'Entrance. Commands: ls, cd, cat. To proceed:  cd cellar && cat scroll' > "$D/scroll"
echo 'A dusty cellar. There is a cabinet.  cd cabinet && cat scroll'        > "$D/cellar/scroll"
echo 'Inside the cabinet, a chest.  cat treasure'                            > "$D/cellar/cabinet/scroll"
echo 'GOLD! You learned cd/ls/cat. You win! ::trophy::'                      > "$D/cellar/cabinet/treasure"`,
		WelcomeMsg: "Learn the shell by exploring. Start:  cd /work/dungeon && cat scroll",
	},
	{
		ID: "learn-git", Title: "Learn: Git", Desc: "practice git, no real repo needed", Category: "Learn",
		Image: "alpine/git:latest",
		SetupScript: `set -e
cd /work
if [ ! -d playground/.git ]; then
  mkdir -p playground && cd playground
  git init -q
  git config user.email learner@boxly.dev
  git config user.name learner
  printf 'chapter one\n' > story.txt && git add . && git commit -qm 'first commit'
  printf 'chapter two\n' >> story.txt && git commit -qam 'second commit'
  printf 'chapter three\n' >> story.txt && git commit -qam 'third commit'
fi
cat > /work/playground/CHALLENGES.txt <<'EOF'
Git practice (in /work/playground):
  1. git log --oneline        # see history
  2. git status               # working tree
  3. echo hi >> story.txt; git diff
  4. git checkout HEAD~1 -- story.txt   # restore older version
  5. git branch feature; git switch feature; commit something
EOF`,
		WelcomeMsg: "Git playground at /work/playground. Open CHALLENGES.txt, then try: git log --oneline",
	},
	{
		ID: "learn-sql", Title: "Learn: SQL", Desc: "solve a mini murder mystery", Category: "Learn",
		Image: "keinos/sqlite3:latest",
		SetupScript: `set -e
cd /work
if [ ! -f mystery.db ]; then
sqlite3 mystery.db <<'EOF'
CREATE TABLE person(id INTEGER PRIMARY KEY, name TEXT, city TEXT);
CREATE TABLE clue(person_id INTEGER, note TEXT);
INSERT INTO person(name,city) VALUES('Alice','Pune'),('Bob','Delhi'),('Carol','Pune');
INSERT INTO clue VALUES(1,'was at the library'),(3,'seen near the docks at midnight'),(2,'out of town');
EOF
fi`,
		WelcomeMsg: "Crack the case:  sqlite3 /work/mystery.db   then:  SELECT * FROM clue JOIN person ON person.id=clue.person_id;",
	},
	{
		ID: "dev-python", Title: "Dev: Python/Data", Desc: "python ready to go", Category: "Dev",
		Image:      "python:3.12-slim",
		WelcomeMsg: "python3 & pip ready. /work is yours. Tip: pip install --user pandas",
	},
	{
		ID: "dev-node", Title: "Dev: Web (Node)", Desc: "node + npm", Category: "Dev",
		Image:      "node:22-slim",
		WelcomeMsg: "node & npm ready. /work is yours. Tip: npm init -y",
	},
}

// Default is used when no template is specified.
const Default = "normal"
