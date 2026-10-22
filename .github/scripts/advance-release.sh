#!/usr/bin/env bash
set -euo pipefail
: "${SOURCE_TAG:?}" "${TARGET_STAGE:?}" "${GITHUB_REPOSITORY:?}" "${STAGE_TOOL:?}"
test "$GITHUB_REF" = refs/heads/main
case "$TARGET_STAGE" in rc|stable) ;; *) echo 'target must be rc or stable' >&2; exit 1 ;; esac
# Validate before a caller-controlled tag reaches git or an API path.
case "$SOURCE_TAG" in v[0-9]*-beta.[0-9]*|v[0-9]*-rc.[0-9]*) ;; *) echo 'select a numbered beta/rc source tag' >&2; exit 1 ;; esac
# The pinned identity tool is the strict grammar check.
"$STAGE_TOOL" advance "$SOURCE_TAG" "$TARGET_STAGE" /dev/null >/dev/null
git fetch --no-tags origin refs/heads/main:refs/remotes/origin/main
git fetch origin --tags
git tag --list > "$RUNNER_TEMP/release-tags"
source_sha=$(git rev-parse --verify "refs/tags/${SOURCE_TAG}^{commit}")
git merge-base --is-ancestor "$source_sha" origin/main
release=$(gh api "repos/$GITHUB_REPOSITORY/releases/tags/$SOURCE_TAG")
printf '%s' "$release" | jq -e --arg tag "$SOURCE_TAG" '.tag_name == $tag and .draft == false' >/dev/null
passed=$(gh run list --repo "$GITHUB_REPOSITORY" --workflow test.yml --branch main --commit "$source_sha" --limit 1 --json conclusion --jq '.[0].conclusion')
test "$passed" = success || { echo 'source has not passed Test on main' >&2; exit 1; }
published=$(gh run list --repo "$GITHUB_REPOSITORY" --workflow release.yml --commit "$source_sha" --status success --limit 100 --json databaseId --jq 'length')
test "$published" -gt 0 || { echo 'source publication has not completed successfully' >&2; exit 1; }
# A retry resumes the same immutable identity, including a publication interrupted
# after the tag was created. It never increments an RC counter on a retry.
base=${SOURCE_TAG%%-*}
if [ "$TARGET_STAGE" = rc ]; then target="$base-rc.1"; else target="$base"; fi
annotation="Advance $SOURCE_TAG to $TARGET_STAGE"
if git show-ref --verify --quiet "refs/tags/$target"; then
 test "$(git rev-parse "refs/tags/$target^{commit}")" = "$source_sha" || { echo 'target tag belongs to another commit; refusing to move it' >&2; exit 1; }
 if [ "$TARGET_STAGE" = rc ]; then
  test "$(git for-each-ref --format='%(contents)' "refs/tags/$target")" = "$annotation" || { echo 'RC identity was not created by this transition' >&2; exit 1; }
 fi
 # Exclude only our resumption target; a newer sibling still refuses the source.
 awk -v target="$target" '$0 != target' "$RUNNER_TEMP/release-tags" > "$RUNNER_TEMP/remaining-tags"
else cp "$RUNNER_TEMP/release-tags" "$RUNNER_TEMP/remaining-tags"; fi
planned=$("$STAGE_TOOL" advance "$SOURCE_TAG" "$TARGET_STAGE" "$RUNNER_TEMP/remaining-tags")
test "$planned" = "$target"
if ! git show-ref --verify --quiet "refs/tags/$target"; then
 git -c user.name="$GITHUB_ACTOR" -c user.email="$GITHUB_ACTOR@users.noreply.github.com" tag -a "$target" "$source_sha" -m "$annotation"
 git push origin "refs/tags/$target"
fi
# GITHUB_TOKEN tag pushes do not start another workflow. Dispatch explicitly on
# that tag so every build, version stamp and acceptance names the selected source.
wait_run() {
 local workflow=$1 ref=$2 sha=$3 since=$4 id status conclusion
 for attempt in $(seq 1 180); do
  id=$(gh run list --repo "$GITHUB_REPOSITORY" --workflow "$workflow" --branch "$ref" --commit "$sha" --limit 20 --json databaseId,createdAt --jq ".[] | select(.createdAt >= \"$since\") | .databaseId" | head -n 1)
  if [ -n "$id" ]; then
   status=$(gh run view "$id" --repo "$GITHUB_REPOSITORY" --json status --jq .status)
   if [ "$status" = completed ]; then
    conclusion=$(gh run view "$id" --repo "$GITHUB_REPOSITORY" --json conclusion --jq .conclusion)
    test "$conclusion" = success || { echo "$workflow run $id failed; repair/rerun it, then resume this transition" >&2; return 1; }
    return 0
   fi
  fi
  sleep 20
 done
 echo "$workflow did not complete in time; inspect its run before resuming" >&2; return 1
}
# Reuse an existing publication run on this exact tag; never dispatch a second
# publisher over an existing release or one that is still publishing.
existing=$(gh run list --repo "$GITHUB_REPOSITORY" --workflow release.yml --branch "$target" --commit "$source_sha" --limit 1 --json databaseId --jq '.[0].databaseId // empty')
if [ -n "$existing" ]; then
 wait_run release.yml "$target" "$source_sha" '1970-01-01T00:00:00Z'
else
 since=$(date -u +%FT%TZ)
 gh workflow run release.yml --repo "$GITHUB_REPOSITORY" --ref "$target" -f "tag=$target"
 wait_run release.yml "$target" "$source_sha" "$since"
fi
# Node acceptance is automatically dispatched by the successful publication.
if [ "${REQUIRE_NODE_ACCEPTANCE:-false}" = true ]; then
 for workflow in container-acceptance.yml installation-acceptance.yml; do
  accepted=false
  for attempt in $(seq 1 180); do
   runs=$(gh run list --repo "$GITHUB_REPOSITORY" --workflow "$workflow" --status success --limit 100 --json databaseId,displayTitle,workflowName,event,headBranch --jq ".[] | select(.displayTitle == .workflowName + \" of $target\") | select(.event == \"workflow_run\" or (.event == \"workflow_dispatch\" and .headBranch == \"main\")) | .databaseId")
   for run in $runs; do
    if gh api "repos/$GITHUB_REPOSITORY/actions/runs/$run/jobs?per_page=100" --jq '[.jobs[].conclusion] | length >= 2 and all(. == "success")' | grep -qx true; then accepted=true; break; fi
   done
   if [ "$accepted" = true ]; then break; fi
   echo "waiting for $workflow acceptance of $target (attempt $attempt)"
   sleep 20
  done
  test "$accepted" = true || { echo "$workflow has not accepted $target on both architectures; resolve it and resume" >&2; exit 1; }
 done
fi
if [ "$TARGET_STAGE" = stable ]; then
 since=$(date -u +%FT%TZ)
 main_sha=$(gh api "repos/$GITHUB_REPOSITORY/commits/main" --jq .sha)
 gh workflow run promote.yml --repo "$GITHUB_REPOSITORY" --ref main -f "tag=$target"
 wait_run promote.yml main "$main_sha" "$since"
fi
printf 'Advanced %s to %s from %s.\n' "$SOURCE_TAG" "$target" "$source_sha" >> "$GITHUB_STEP_SUMMARY"
