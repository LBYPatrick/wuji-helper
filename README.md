# Wuji Helper

A terminal UI to upgrade or downgrade selected Wuji gloves using an OTA ZIP
package supplied by Wuji, such as `~/gboard-v0.10.1.ota.zip`.

It uses the [official Wuji CLI](https://github.com/wuji-technology/wuji-cli)
for USB/network discovery, device communication, package verification, and
flashing. Firmware transport is not reimplemented here.

## Setup

Firmware updates support Linux x86_64/ARM64 and Apple Silicon macOS, matching
[Wuji CLI's supported platforms](https://github.com/wuji-technology/wuji-cli#prerequisites).
The helper also compiles for Intel macOS, but Wuji's official installer does not
provide an Intel Mac CLI; flashing there requires a compatible Wuji CLI supplied
separately.

Requires `make`, a terminal, and standard shell tools (`sh`, `curl`, `tar`, and
`sha256sum` or `shasum`). `make install` installs missing Go and Wuji CLI tools
using native `sh`, without Homebrew or another package manager. It downloads
the current stable Go archive from the official Go distribution and verifies
its SHA-256 checksum, then uses Wuji's official CLI installer. Go is stored in
`~/.local/lib/go<VERSION>` with links in `~/.local/bin`; Wuji is installed as
`~/.local/bin/wuji`. No sudo is needed, and optional Wuji agent skills are skipped.

Existing tools on `PATH` are reused. Go must be 1.22 or newer (older installations
must be upgraded), and Wuji CLI must support `upgrade --file`. Development was
verified with Wuji CLI `2026.9.22`. On macOS,
Xcode Command Line Tools provide `make` and the C compiler needed for race tests;
on Linux, install GNU Make and a C compiler for development tests.
Install dependencies and build:

```sh
make install
make build
export PATH="$PATH:$HOME/.local/bin"
./bin/wuji-helper
```

`make build`, `make build-cross`, and `make run` automatically run `make install`
first, so the explicit install step is optional. Make adds `~/.local/bin` to its
own `PATH`; add the export above to your shell profile for direct use outside Make.
Initial installation requires internet access; repeat runs reuse installed tools.

Alternatively, run `make run`, or specify an executable explicitly:

```sh
go run . --wuji /path/to/wuji
```

Power on the gloves and connect them via USB or the same local network.

## Workflow

The workspace keeps the current step and keyboard hints visible throughout.
It fits an 80×24 terminal and centers the content on larger screens. Navigation
and selection respond immediately, without animation. Like Ashley, directional
focus follows the screen layout: **↑/↓** moves between rows and **←/→** moves
across buttons. Arrows stop at the edges; **Tab/Shift+Tab** also moves focus.
**Enter** activates the focused control and **Esc** goes back. In the package
path field, **←/→** edits the text cursor instead of moving focus.

1. **Choose gloves.** Discovery checks USB and the local network. Use **↑/↓**
   to browse, **Space** to select a glove, and **A** to select or clear all ready
   gloves. The list shows handedness, serial, firmware, and transport; the detail
   panel shows the focused device's address or connection problem. Unavailable
   devices remain visible but cannot be selected. **R** rescans, preserving
   selections for gloves that are still reachable. **Enter** continues;
   **↓** past the last device moves to the action buttons; **↑** returns to
   the list. **←/→** chooses a button. **Tab** also moves to the actions.
2. **Choose a package.** Paste the path to the Wuji OTA ZIP and press **Enter**
   or choose **Review package**. `~/`, relative paths, spaces, and quoted paths
   are supported. The helper checks ZIP integrity and keeps a private temporary
   copy. Errors appear inline so you can edit the path and retry. **Esc** returns
   to glove selection without losing the path or selections. ZIP packages and
   expanded contents are limited to 512 MiB; raw `.bin` files are not accepted.
3. **Review.** Check the package, selected gloves, current firmware, and declared
   package version. **PgUp/PgDn** scrolls the full plan, including its source path
   and SHA-256 fingerprint. Check **I reviewed the targets and firmware** with
   **Space**, then **↓** to **Install firmware** and press **Enter**. Installation
   stays disabled until checked. **Esc** edits the package; **Cancel** exits
   without flashing. Returning to review requires confirmation again.
4. **Install.** A device status panel tracks waiting, installing, completed,
   failed, and unstarted gloves alongside live CLI output. The helper rechecks
   each glove's identity, handedness, and firmware before calling
   `wuji upgrade --sn SERIAL --file SNAPSHOT --yes`. The official CLI verifies
   the manifest, firmware digest, and device compatibility. Keep gloves powered
   and connected and close other applications using them. Installation runs
   sequentially and stops on the first failure. Earlier successful updates are
   not rolled back. Successfully flashed gloves reboot; same-version packages
   may be skipped.
5. **Read the report.** Use **←** for device status and **→** for output
   (**Tab** also switches panels);
   **↑/↓** or **PgUp/PgDn** scrolls the focused panel. **End** follows the latest
   output. A completed device means the CLI finished; its output distinguishes
   installed from skipped firmware. **Enter** or **Q** exits the final report.

An older package downgrades firmware; a newer package upgrades it. This tool
installs your local package and does not choose or download a catalog version.
The displayed manifest version is metadata; the CLI performs firmware validation.
For upstream behavior, see [Wuji's firmware documentation](https://docs.wuji.tech/docs/en/wuji-cli/latest/firmware-upgrade/).

Ctrl+C exits before installation. During installation, the TUI ignores Ctrl+C
until the CLI finishes, to avoid interrupting an active flash. On the report
screen, scroll with arrow/Page Up/Page Down keys and press Enter or `q` to exit.
A failed operation exits with status 1. Temporary package copies are removed on
normal exit. After a forced process termination, leftover copies may remain in
the system temporary directory under `wuji-helper-*`.

## Development

Run `make help` (or just `make`) to list available targets and descriptions.

```sh
make format
make format-check
make test
make build
make build-cross
```

`make build` creates an executable for the current machine. `make build-cross`
creates `bin/wuji-helper-{linux,darwin}-{amd64,arm64}` without requiring a C
compiler. Use the executable matching the target operating system and CPU.

The CI workflow runs formatting checks, race-enabled tests with a 70% statement
coverage minimum, vet, builds, and executable smoke tests on native Linux
x86_64, Linux ARM64, and Apple Silicon macOS runners. It checks Go 1.22 and the
current stable Go version. Tests use a fake CLI and need no gloves or Wuji
installation; build targets install Wuji if missing. The workflow runs after
pushing to GitHub or opening a pull request.

Local validation was performed on Linux x86_64, including Go 1.22 and Go 1.27;
Linux ARM64 and both macOS executables were cross-compiled. Dependency installation,
race tests, and native/cross-builds were also verified on Apple Silicon macOS.
Native macOS CI and real glove flashing have not been run locally.

Tests cover package validation and snapshots, path expansion, device metadata,
explicit serial targeting, subprocess failures, and simulated terminal workflows
for confirmation, cancellation, and stopping a multi-glove plan after failure.
They use a fake CLI and do not flash hardware. Real device discovery/flashing
still needs validation with connected Wuji gloves and an official OTA package.
Build output is stored in the ignored `bin/` directory; `make clean` removes it.
