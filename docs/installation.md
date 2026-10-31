# Installation

All the ways to install stackctl. See the [README](../README.md) for an overview and the [documentation map](../README.md#documentation).

## Quick install (Linux / macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/omattsson/stackctl/main/install.sh | sudo bash
```

The script supports Linux and macOS, on amd64 and arm64. It installs to `/usr/local/bin` by default. Set `INSTALL_DIR` to use another directory. This needs no `sudo` when you own the directory:

```bash
curl -fsSL https://raw.githubusercontent.com/omattsson/stackctl/main/install.sh | INSTALL_DIR=$HOME/.local/bin bash
```

## Homebrew (macOS / Linux)

```bash
brew install --cask omattsson/tap/stackctl
```

No compiler is needed. The cask removes the macOS quarantine attribute from the binary, because the binary is not signed or notarized. The cask needs Homebrew 6.0.13 or later (`brew update`).

If you installed the old formula, remove it once before the first cask install:

```bash
brew uninstall --formula stackctl && brew install --cask omattsson/tap/stackctl
```

Homebrew installs stable releases only. To install a release candidate (for example `vX.Y.Z-rc.1`), use the release binaries below.

## From release binaries

Download the latest binary for your platform from [Releases](https://github.com/omattsson/stackctl/releases), then:

```bash
tar -xzf stackctl_*.tar.gz
sudo install -m 755 stackctl /usr/local/bin/stackctl
```

Archive names are `stackctl_<version>_<os>_<arch>`. Linux and macOS archives are `.tar.gz`. Windows archives are `.zip`. Each release also has a `checksums.txt` file with SHA-256 checksums. Verify a download before you install it:

```bash
sha256sum --check --ignore-missing checksums.txt   # macOS: shasum -a 256 --check --ignore-missing checksums.txt
```

Release candidates (for example `vX.Y.Z-rc.1`) are GitHub pre-releases. They are not "Latest", and the quick install script and Homebrew do not install them. Download the archive from the release page of the tag, for example `https://github.com/omattsson/stackctl/releases/tag/vX.Y.Z-rc.1`. On macOS, remove the quarantine attribute before the first run: `xattr -d com.apple.quarantine stackctl`.

## From source

```bash
git clone https://github.com/omattsson/stackctl.git
cd stackctl/cli
make build
sudo cp bin/stackctl /usr/local/bin/
```

`make build` sets the version, commit and date with linker flags. A plain `go build -o bin/stackctl .` also works, but then `stackctl version` prints development values.

A container image is available at `ghcr.io/omattsson/stackctl`. It is published for each `v*` tag.
