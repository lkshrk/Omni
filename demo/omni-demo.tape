Output docs/assets/omni-demo.gif

Require go
Require git
Require sqlite3

Set Shell "bash"
Set FontSize 18
Set FontFamily "Menlo"
Set LetterSpacing 0
Set Width 1280
Set Height 760
Set Padding 18
Set BorderRadius 10
Set WindowBar Rings
Set Theme "TokyoNight"
Set TypingSpeed 28ms
Set Framerate 24
Set PlaybackSpeed 1.0

# ── Build & prepare demo (hidden) ──
Hide
Type "mkdir -p /private/tmp/omni-vhs-bin && GOCACHE=/private/tmp/omni-gocache go build -o /private/tmp/omni-vhs-bin/omni ./cmd/omni" Enter
Sleep 500ms
Type "chmod +x demo/prepare-vhs-demo.sh && demo/prepare-vhs-demo.sh /private/tmp/omni-vhs-demo /private/tmp/omni-vhs-bin/omni" Enter
Sleep 500ms
Type "export OMNI_CONFIG=/private/tmp/omni-vhs-demo/settings.json OMNI_CACHE_DIR=/private/tmp/omni-vhs-demo/cache OMNI_HOSTNAME=demo-macbook HOME=/private/tmp/omni-vhs-demo/home PATH=/private/tmp/omni-vhs-demo/bin:/private/tmp/omni-vhs-bin TERM_PROGRAM=WezTerm COLORTERM=truecolor" Enter
Type "omni ui" Enter
Sleep 500ms

# ── Bootstrap (visible briefly) ──
Show
Sleep 1.5s

# ── Cut bootstrap wait + loading ──
Hide
Sleep 20s

# ── Dashboard (4s) ── browse health rows
Show
Sleep 500ms
Type "j"
Sleep 500ms
Type "j"
Sleep 500ms
Type "j"
Sleep 2.5s

# ── Tools tab (4s) ── browse down
Tab
Sleep 500ms
Type "j"
Sleep 500ms
Type "j"
Sleep 3s

# ── Command palette popup (4s) ──
Type ":"
Sleep 4s

# ── Tools with provider filter (4s) ──
Escape
Sleep 200ms
Type "]"
Sleep 3.8s

# ── Tools search (4s) ── type "pre" → filters to prettier + pre-commit
Type "/pre"
Sleep 3s
Escape
Sleep 1s

# ── Dots tab (4s) ── browse entries
Tab
Sleep 500ms
Type "j"
Sleep 500ms
Type "j"
Sleep 3s

# ── Groups tab (4s) ── browse groups
Tab
Sleep 500ms
Type "j"
Sleep 500ms
Type "j"
Sleep 3s

# ── Settings tab (4s) ── browse settings
Tab
Sleep 500ms
Type "j"
Sleep 500ms
Type "j"
Sleep 3s

# ── Done ──
Hide
Type "q"
Sleep 400ms
Type "q"
Sleep 1s
