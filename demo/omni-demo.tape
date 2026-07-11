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
Sleep 22s

# ── Dashboard: Tool Updates (default cursor) ──
Show
Sleep 3s

# ── Dashboard: Tool Sync ──
Type "j"
Sleep 2.5s

# ── Dashboard: Agents attention ──
Type "j"
Type "j"
Sleep 3s

# ── Tools: ripgrep update row with description ──
Tab
Sleep 800ms
Sleep 3s

# ── Command palette: migrate-nvm ──
Type ":"
Sleep 300ms
Type "mig"
Sleep 3.5s
Escape
Sleep 300ms

# ── Tools search: ripgrep ──
Type "/rip"
Sleep 3.5s
Escape
Sleep 500ms

# ── Dots: synced entry with hints ──
Tab
Sleep 800ms
Sleep 3s

# ── Agents: plugin update with marketplace description ──
Tab
Sleep 1s
Sleep 3.5s

# ── Agents: MCP chip + grafana transport details ──
Right
Sleep 500ms
Sleep 3.5s

# ── Groups: host assignment details ──
Tab
Sleep 800ms
Sleep 3.5s