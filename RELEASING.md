# Releasing hali (OSS)

This project uses a simple workflow:

1. Do work on a feature branch.
2. Open a PR into `main`.
3. Merge after review and CI passes.
4. Create a version tag on `main`.
5. Push the tag to trigger the release workflow (if configured) or build artifacts manually.

No separate release branch is required for normal releases.

## 1) Feature branch to main

```sh
git checkout main
git pull origin main
git checkout -b feat/my-change

# ... make changes ...

git add .
git commit -m "feat: my change"
git push -u origin feat/my-change
```

Open PR: `feat/my-change` -> `main`.

After approval and green CI, merge into `main`.

## 2) Tag a release from main

Pick the exact `main` commit to release (usually HEAD after merge), then tag it.

```sh
git checkout main
git pull origin main

# choose version (semver), for example v1.4.0
git tag -a v1.4.0 -m "Release v1.4.0"
git push origin v1.4.0
```

## 3) Build Linux artifacts (manual path)

If release automation is not yet configured, build on Linux/WSL:

```sh
installer/linux/build-linux.sh
```

Expected output in `bin/`:

- `hali-linux-amd64`
- `halid-linux-amd64`
- `hali-linux-amd64.tar.gz`
- `hali_<version>_amd64.deb` (only when `dpkg-deb` exists)

## 4) Verify `.deb` before upload

Always verify file type before publishing:

```sh
file bin/hali_<version>_amd64.deb
```

A valid Debian package should report a Debian archive/ar package format.

Do not rename raw ELF binaries to `.deb`.

For example, `hali-linux-amd64` is a Linux executable and must be published as a binary (or inside tar.gz), not as a Debian package.

## 5) Upload release artifacts

Attach the real package and binaries to the GitHub Release for the tag:

- `hali_<version>_amd64.deb`
- `hali-linux-amd64.tar.gz`
- `hali-linux-amd64`
- `halid-linux-amd64`

Optional but recommended:

```sh
sha256sum bin/hali_<version>_amd64.deb bin/hali-linux-amd64.tar.gz bin/hali-linux-amd64 bin/halid-linux-amd64 > bin/SHA256SUMS.txt
```

Upload `SHA256SUMS.txt` as a release asset.

## 6) Post-release smoke test (Ubuntu)

```sh
sudo apt install ./hali_<version>_amd64.deb
hali --help
systemctl status halid
```

## Notes

- CI in this repository currently validates and builds, but does not create `.deb` release assets by default.
- If you add a tag-triggered GitHub Actions release workflow, keep this document as the human runbook and fallback path.
