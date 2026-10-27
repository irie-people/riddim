#!/bin/sh
# Refuses a commit subject or pull request title that isn't a Conventional Commit
# (https://www.conventionalcommits.org/en/v1.0.0/) and prints the nearest one that passes.
# release-please reads the subjects to pick the next version and write CHANGELOG.md.
#
#   sh .github/conventional-commit.sh FILE          as the commit-msg hook: FILE holds the message
#   sh .github/conventional-commit.sh --title TEXT  a pull request title (the commits workflow)
#   sh .github/conventional-commit.sh --rev REV     a commit already made (the commits workflow)
#
# Merges, git's own Revert and Reapply subjects, and fixup!, squash! and amend! commits pass.
set -eu

types="feat fix docs style refactor perf test build ci chore revert"

case ${1:-} in
--title)
	what="pull request title"
	subject=${2:-}
	;;
--rev)
	rev=$(git rev-parse --short "${2:?--rev needs a commit}")
	what="commit $rev"
	# A merge has two parents or more.
	set -- $(git rev-list --parents -n 1 "$rev")
	if [ $# -gt 2 ]; then exit 0; fi
	subject=$(git log -1 --format=%B "$rev" | head -n 1)
	;;
"" | -*)
	echo "usage: sh $0 FILE | --title TEXT | --rev REV" >&2
	exit 2
	;;
*)
	what=commit
	file=$1
	# git merge runs this hook too, and MERGE_HEAD is there while a merge commits.
	if [ -e "$(git rev-parse --git-path MERGE_HEAD)" ]; then exit 0; fi
	# The subject is the first line that isn't blank or a comment, above any scissors line.
	subject=$(tr -d '\r' <"$file" | sed -e '/^# -* >8 -*$/,$d' -e '/^#/d' -e '/^[[:space:]]*$/d' | head -n 1)
	;;
esac

case $subject in
"") exit 0 ;; # git refuses an empty message itself
"Revert \""*\" | "Reapply \""*\" | "fixup! "* | "squash! "* | "amend! "*) exit 0 ;;
esac
passes() { printf '%s\n' "$1" | grep -Eq "^($(echo $types | tr ' ' '|'))(\([^()]+\))?!?: [^[:space:]]"; }
if passes "$subject"; then exit 0; fi

# guess names the type a word most likely means, or nothing.
guess() {
	case $(printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr -cd '[:alpha:]') in
	feat | feature | features | add | adds | added | new | create | creates | implement | implements | \
		introduce | introduces | support | supports | allow | allows | enable | enables) echo feat ;;
	fix | fixes | fixed | bugfix | hotfix | correct | corrects | repair | repairs | resolve | resolves | \
		handle | handles | prevent | prevents) echo fix ;;
	docs | doc | document | documents | readme) echo docs ;;
	style | format | formats | lint) echo style ;;
	refactor | refactors | rename | renames | move | moves | extract | extracts | simplify | simplifies | \
		split | splits) echo refactor ;;
	perf | performance | optimize | optimise | speed) echo perf ;;
	test | tests | testing) echo test ;;
	build | bump | bumps | upgrade | upgrades | deps) echo build ;;
	ci) echo ci ;;
	chore | chores) echo chore ;;
	revert | reverts) echo revert ;;
	esac
}

# The nearest subject that passes: mend a prefix that names a type, or guess one from the first word.
s=$(printf '%s' "$subject" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
head=${s%%:*} type= mods= desc=$s
if [ "$head" != "$s" ] && printf '%s\n' "$head" | grep -Eq '^[A-Za-z]+[[:space:]]*(\([^()]*\))?[[:space:]]*!?[[:space:]]*$'; then
	word=${head%%[(! ]*}
	type=$(guess "$word")
	if [ -n "$type" ]; then
		mods=$(printf '%s' "${head#"$word"}" | tr -d '[:space:]' | sed 's/()//')
		desc=$(printf '%s' "${s#*:}" | sed 's/^[[:space:]]*//')
	fi
fi
if [ -z "$type" ]; then type=$(guess "${s%% *}"); fi
fix="${type:-chore}$mods: ${desc:-<what it changes>}"
if ! passes "$fix"; then fix="${type:-chore}: ${desc:-<what it changes>}"; fi

{
	echo "$what: not a Conventional Commit subject."
	echo "  got:   $subject"
	echo "  write: $fix"
	echo "The form is <type>(<optional scope>): <description>. Use the type that fits, in lower case"
	echo "(release-please reads it that way): $types."
	echo "feat adds behaviour and fix repairs it; both go in CHANGELOG.md. A ! before the colon marks a breaking change."
	case $what in
	commit) echo "Nothing was committed. Run the same commit again with its first line changed to the line after write: (your message is kept in $file)." ;;
	"pull request title") echo "Edit the pull request title to the line after write:. A squash merge makes it the commit on main." ;;
	*) echo "Reword it: git commit --amend if it is the last commit, else git rebase -i $rev~1 and mark it reword; then git push --force-with-lease." ;;
	esac
	echo "The rules: https://www.conventionalcommits.org/en/v1.0.0/"
} >&2
exit 1
