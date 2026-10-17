# Releasing

Releases run in GitHub Actions on a `vX.Y.Z` tag (and from the weekly `refresh-prices`
workflow, which starts `release.yml` with `workflow_dispatch` so that npm sees
`release.yml` as the publisher). They publish GitHub Release binaries with goreleaser and the npm
packages with trusted publishing (OIDC). There are no long-lived tokens or repo
secrets. The last step moves the major tag (`v1`) to the release, so
`uses: badmintoncryer/cfn-idle-cost@v1` in a workflow picks it up.

## One-time setup (npm)

Trusted publishing can only be configured for packages that already exist, so
the very first version is published from a laptop. Needs npm >= 11.10 and 2FA on
the npm account.

```sh
npm login
scripts/npm-publish.sh 0.0.1          # creates the packages (win32-x64: see below)
pkgs="cfn-idle-cost cdk-idle-cost"
for p in darwin-arm64 darwin-x64 linux-x64 linux-arm64; do pkgs="$pkgs cfn-idle-cost-$p"; done
for p in $pkgs; do
  npm trust github "$p" --repo badmintoncryer/cfn-idle-cost --file release.yml --allow-publish -y
done
```

Then, on npmjs.com, set each package's publishing access to "Require two-factor
authentication and disallow tokens", so trusted publishing is the only way in.

## Cutting a release

```sh
git tag v0.1.0 && git push origin v0.1.0
```

A new platform (a new `cfn-idle-cost-<os>-<cpu>` package) needs the same
first-publish + `npm trust` step before its first release.

## Windows package

npm's spam check rejects the name `cfn-idle-cost-win32-x64` (E403), so the npm
packages don't include Windows yet; the GitHub Release still has the Windows
binary. Once npm support allows the name, add `windows/amd64:win32/x64` back to
`TARGETS` in `scripts/npm-publish.sh`, publish it once from a laptop and run
`npm trust` for it like the others.
