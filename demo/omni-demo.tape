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
Sleep 1s
Type "chmod +x demo/prepare-vhs-demo.sh && demo/prepare-vhs-demo.sh /private/tmp/omni-vhs-demo /private/tmp/omni-vhs-bin/omni" Enter
Sleep 1s
Type "export OMNI_CONFIG=/private/tmp/omni-vhs-demo/settings.json OMNI_CACHE_DIR=/private/tmp/omni-vhs-demo/cache OMNI_HOSTNAME=demo-macbook HOME=/private/tmp/omni-vhs-demo/home PATH=/private/tmp/omni-vhs-demo/bin:/private/tmp/omni-vhs-bin TERM_PROGRAM=WezTerm COLORTERM=truecolor" Enter
Type "omni ui" Enter
Sleep 5s
Show
Sleep 2s

Type "j"
Sleep 800ms

Type "A"
Sleep 1.8s

Escape
Sleep 700ms

Type "j"
Sleep 700ms

Tab
Sleep 2.2s

Tab
Sleep 2.2s

Tab
Sleep 2.2s

Tab
Sleep 1.2s

Type "]"
Sleep 600ms

Type "/term"
Sleep 1.2s
Enter
Sleep 2.4s

Hide
Type "q"
Sleep 400ms
Type "q"
Sleep 1s
