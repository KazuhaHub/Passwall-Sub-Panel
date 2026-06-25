// vite build runs with emptyOutDir:true, so it wipes internal/web/dist/ — including
// the tracked .gitkeep placeholder. That placeholder is what lets `go:embed all:dist`
// (internal/web/web.go) find a non-empty directory on a fresh clone and in the Test CI
// job (which does NOT build the SPA, only runs `go vet`/`go test`). If the build leaves
// the dir without it, `git add -A` stages the deletion and CI breaks with
// "pattern all:dist: no matching files found". Re-create it after every build so that
// can't happen. Runs automatically as the npm `postbuild` hook. cwd is web-react/.
import { writeFileSync } from 'node:fs'

const content = `# Keeps internal/web/dist/ present on a fresh clone so \`go:embed all:dist\`
# (internal/web/web.go) has a non-empty directory and \`go build ./cmd/panel\`
# compiles before the SPA is built. The real bundle (npm run build) lands here
# and is git-ignored; only this placeholder is tracked (see .gitignore).
`

writeFileSync('../internal/web/dist/.gitkeep', content)
