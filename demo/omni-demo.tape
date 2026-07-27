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

Hide
Type "mkdir -p /private/tmp/omni-vhs-bin && GOCACHE=/private/tmp/omni-gocache go build -o /private/tmp/omni-vhs-bin/omni ./cmd/omni" Enter
Sleep 500ms
Type "chmod +x demo/prepare-vhs-demo.sh && demo/prepare-vhs-demo.sh /private/tmp/omni-vhs-demo /private/tmp/omni-vhs-bin/omni" Enter
Sleep 500ms
Type "export OMNI_CONFIG=/private/tmp/omni-vhs-demo/settings.json OMNI_CACHE_DIR=/private/tmp/omni-vhs-demo/cache OMNI_HOSTNAME=demo-macbook HOME=/private/tmp/omni-vhs-demo/home PATH=/private/tmp/omni-vhs-demo/bin:/private/tmp/omni-vhs-bin TERM_PROGRAM=WezTerm COLORTERM=truecolor" Enter
Type "omni ui" Enter
Sleep 500ms

Show
Sleep 1.5s

Hide
Sleep 22s

Show
Sleep 3s

Type "j"
Sleep 2.5s

Type "j"
Type "j"
Sleep 3s

Tab
Sleep 800ms
Sleep 3s

Type ":"
Sleep 300ms
Type "mig"
Sleep 3.5s
Escape
Sleep 300ms

Type "/rip"
Sleep 3.5s
Escape
Sleep 500ms

Tab
Sleep 800ms
Sleep 3s

Tab
Sleep 1s
Sleep 3.5s

Right
Sleep 500ms
Sleep 3.5s

Tab
Sleep 800ms
Sleep 3.5s
