#!/usr/bin/env bash
# Build per-platform binaries and publish them as npm packages:
#   cfn-idle-cost-<platform>-<arch>  (binary)
#   cfn-idle-cost                    (launcher, optionalDependencies on the above)
#   cdk-idle-cost                    (alias)
# Usage: scripts/npm-publish.sh <version> [--dry-run]
set -euo pipefail
VERSION=${1:?version}; shift
OUT=${NPM_OUT:-$(mktemp -d)}
TARGETS="darwin/arm64:darwin/arm64 darwin/amd64:darwin/x64 linux/amd64:linux/x64 linux/arm64:linux/arm64 windows/amd64:win32/x64"
OPT=""
for t in $TARGETS; do
  IFS=: read -r go node <<<"$t"
  goos=${go%/*} goarch=${go#*/} os=${node%/*} cpu=${node#*/}
  name="cfn-idle-cost-$os-$cpu"; dir="$OUT/$name"; exe=cfn-idle-cost; [ "$os" = win32 ] && exe=$exe.exe
  mkdir -p "$dir/bin"
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -ldflags "-s -w -X main.version=$VERSION" -o "$dir/bin/$exe" .
  cat >"$dir/package.json" <<JSON
{"name":"$name","version":"$VERSION","description":"cfn-idle-cost binary for $os-$cpu","license":"Apache-2.0",
 "repository":{"type":"git","url":"git+https://github.com/badmintoncryer/cfn-idle-cost.git"},"os":["$os"],"cpu":["$cpu"],"files":["bin"]}
JSON
  npm publish "$dir" --access public "$@"
  OPT="$OPT\"$name\":\"$VERSION\","
done
for p in cfn-idle-cost cdk-idle-cost; do
  cp -R "npm/$p" "$OUT/$p"; cp README.md LICENSE "$OUT/$p/"
done
deps="{${OPT%,}}"
cat >"$OUT/cfn-idle-cost/package.json" <<JSON
{"name":"cfn-idle-cost","version":"$VERSION","description":"Monthly idle cost (what you pay with zero traffic) of an AWS CDK app or CloudFormation template",
 "license":"Apache-2.0","repository":{"type":"git","url":"git+https://github.com/badmintoncryer/cfn-idle-cost.git"},"keywords":["aws","aws-cdk","cdk","cloudformation","cost","pricing","finops"],
 "bin":{"cfn-idle-cost":"bin/cfn-idle-cost.js"},"optionalDependencies":$deps}
JSON
npm publish "$OUT/cfn-idle-cost" --access public "$@"
cat >"$OUT/cdk-idle-cost/package.json" <<JSON
{"name":"cdk-idle-cost","version":"$VERSION","description":"Alias of cfn-idle-cost: monthly idle cost of an AWS CDK app",
 "license":"Apache-2.0","repository":{"type":"git","url":"git+https://github.com/badmintoncryer/cfn-idle-cost.git"},"keywords":["aws","aws-cdk","cdk","cloudformation","cost","pricing","finops"],
 "bin":{"cdk-idle-cost":"bin/cdk-idle-cost.js"},"dependencies":{"cfn-idle-cost":"$VERSION"}}
JSON
npm publish "$OUT/cdk-idle-cost" --access public "$@"
