---
name: release
description: This skill should be used when the user asks to "release", "publish", "ship", "make a release", "release a new version", "bump version", or refers to the vuek8 release process. Handles the full release workflow including changelog updates, building, signing, notarization, GitHub release publishing, website deployment, and link updates.
version: 1.0.0
---

# Vue.k8 Release Skill

This skill handles the complete release workflow for the vuek8 project. **Do not skip the changelog step** — users explicitly want release notes for every release so they appear in the in-app "What's new" toast.

## Decide the Version

Before starting, determine the next version. The user usually says "patch" / "minor" / "major" / specific version. If unclear, ask. Look at the current version in `Makefile` or via `git tag --sort=-version:refname | head -1`.

- **Patch** (e.g. 0.5.3 → 0.5.4): bug fixes only
- **Minor** (e.g. 0.5.3 → 0.6.0): user-facing features
- **Major** (e.g. 0.5.3 → 1.0.0): breaking changes (rare)

In this project the user usually says "fais une mineure" or "patch" loosely — clarify if ambiguous, otherwise default to incrementing the minor or patch version based on what was just shipped.

## Steps (in order)

### 1. Update the in-app changelog

Edit `internal/web/static/app.js` and prepend a new entry at the top of the `changelog` array. Pattern:

```javascript
const changelog = [
  { version: 'X.Y.Z', items: [
    'User-facing change one',
    'Another change',
    // ...
  ]},
  // ... existing entries
];
```

**Rules for changelog items:**
- Write user-facing summaries, not commit messages — describe what the user can now do
- One sentence per item, present tense, no trailing period
- 3-7 items is typical; merge related changes
- Skip purely internal/refactor changes
- Skip changes that don't affect the user (CI tweaks, README typos, etc.)
- Look at `git log --oneline <last-tag>..HEAD` to see what changed since the last release

### 2. Stage, commit, push

```bash
git add -A
git commit -m "<descriptive message>"
git push origin main
```

Commit message should describe the actual code changes (use multi-line if needed). Don't include "Co-Authored-By" lines (project rule in CLAUDE.md).

### 3. Run the release

```bash
make release VERSION=X.Y.Z
```

Run in **background** (long-running, takes 5-10 min). The Makefile handles: cross-platform binaries, .app bundle, code signing, DMG creation, Apple notarization, GitHub release with assets.

**Known flake:** `create-dmg` sometimes fails with `AppleScript Finder error -10006`. If this happens:
1. Clean up: `rm -f dist/rw.*.dmg`
2. Retry `make release VERSION=X.Y.Z`
3. If it fails 3+ times, fall back to building DMG with `--skip-jenkins` flag manually:
   ```bash
   create-dmg --volname "Vue.k8" --window-pos 200 120 --window-size 660 400 \
     --icon-size 120 --icon "Vue.k8.app" 160 190 --app-drop-link 500 190 \
     --no-internet-enable --skip-jenkins \
     --codesign "Developer ID Application: Antoine MELKI (9H445KDYMD)" \
     dist/Vue.k8-X.Y.Z.dmg dist/Vue.k8.app
   ```
   Then: `xcrun notarytool submit dist/Vue.k8-X.Y.Z.dmg --keychain-profile "vuek8-notary" --wait`
   Then: `xcrun stapler staple dist/Vue.k8-X.Y.Z.dmg`
   Then: `cp dist/Vue.k8-X.Y.Z.dmg dist/release/` and `gh release create vX.Y.Z dist/release/Vue.k8-X.Y.Z.dmg dist/release/vuek8-X.Y.Z-* --title "vX.Y.Z" --notes "See https://vuek8.app for details."`

### 4. Update download links

After release succeeds, replace the old version with the new one in:
- `website/index.html` (3 download URLs)
- `README.md` (4 download URLs)

Use Edit with `replace_all: true` and old version as old_string.

### 5. Commit, push, deploy

```bash
git add website/index.html README.md
git commit -m "Update download links to vX.Y.Z"
git push origin main
make deploy-site
aws --profile vuek8 --region eu-west-3 s3 cp website/open.html s3://vuek8-releases/open --content-type "text/html"
```

The last `aws s3 cp` is required because the deep-link landing page is served at the extensionless `/open` path on CloudFront. `make deploy-site` only syncs `.html` files.

## Re-releasing the Same Version (Overwrite)

If the user wants to overwrite an existing release (e.g. forgot something):

```bash
gh release delete vX.Y.Z --yes
git push origin --delete vX.Y.Z
git tag -d vX.Y.Z
```

Then proceed with steps 1-5 normally. Skip step 4 if the website already points to that version.

## Common Pitfalls

- **Forgetting the changelog**: This is the #1 thing the user reminds about. Always do step 1.
- **Forgetting the extensionless `open` upload**: After `make deploy-site`, the deep-link landing page won't work because S3/CloudFront looks for `/open` not `/open.html`.
- **Network flakes on `git push`**: Just retry, don't panic.
- **Notarization wait**: Apple's notary service typically takes 1-3 minutes. Don't kill the process early.
- **Hook blocking `make release`**: There's a PreToolUse hook that asks the user to confirm release commands. The hook now uses `permissionDecision: "ask"` (not hard-block) — just wait for user approval.

## Reporting Back

After a successful release, report:
- Version released
- Link to the GitHub release
- Confirmation that website is deployed
